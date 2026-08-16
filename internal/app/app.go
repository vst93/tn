package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	glamansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	termansi "github.com/charmbracelet/x/ansi"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/vst93/tn/internal/storage"
)

var (
	successSty = lipgloss.NewStyle().Foreground(green).Bold(true)
	editBadge  = lipgloss.NewStyle().Bold(true).Background(accent).Foreground(bg)
	keywordSty = lipgloss.NewStyle().Foreground(accent).Bold(true)
)

type pane int

const (
	treePane pane = iota
	contentPane
)

type mode int

const (
	modeNormal mode = iota
	modeEdit
	modePrompt
	modeConfirm
	modeHelp
	modeSearch
	modeExport
	modeSearchGlobal
	modeQuickOpen
	modeTag
	modeTagFilter
	modeCommand
	modeWebdavConfig
	modeWebdavSync
)

type promptKind int

const (
	promptNote promptKind = iota
	promptDir
	promptRename
	promptGotoLine
)

type sortMode int

const (
	sortByName sortMode = iota
	sortByModified
	sortByCreated
)

type flatNode struct {
	node  *storage.Node
	depth int
}

// FrontMatter holds the YAML metadata stored at the top of a note file.
type FrontMatter struct {
	Title   string
	Tags    []string
	Created string
	Pinned  bool
}

// parseFrontMatter extracts YAML metadata from the top of a note. When the
// file has no front matter it returns empty metadata and the unchanged body.
func parseFrontMatter(content string) (FrontMatter, string) {
	var meta FrontMatter
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return meta, content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, content
	}
	for _, line := range lines[1:end] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "title":
			meta.Title = unquote(value)
		case "created":
			meta.Created = unquote(value)
		case "tags":
			meta.Tags = parseFrontTags(value)
		case "pinned":
			meta.Pinned = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseFrontTags(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimPrefix(value, "["))
	value = strings.TrimSpace(strings.TrimSuffix(value, "]"))
	var tags []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(value, ",") {
		part = unquote(strings.TrimSpace(part))
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		tags = append(tags, part)
	}
	return tags
}

func parseTagsInput(raw string) []string {
	return parseFrontTags(raw)
}

// writeFrontMatter serializes a note body with YAML front matter. Fields are
// only emitted when present so a note with no tags stays minimal.
func writeFrontMatter(body string, meta FrontMatter) string {
	var b strings.Builder
	b.WriteString("---\n")
	if meta.Title != "" {
		b.WriteString("title: " + meta.Title + "\n")
	}
	if len(meta.Tags) > 0 {
		b.WriteString("tags: [" + strings.Join(meta.Tags, ", ") + "]\n")
	}
	if meta.Created != "" {
		b.WriteString("created: " + meta.Created + "\n")
	}
	if meta.Pinned {
		b.WriteString("pinned: true\n")
	}
	b.WriteString("---\n\n")
	body = strings.TrimLeft(body, "\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func containsTag(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, query) {
			return true
		}
	}
	return false
}

func ensureTag(content, tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return content
	}
	meta, body := parseFrontMatter(content)
	for _, existing := range meta.Tags {
		if strings.EqualFold(existing, tag) {
			return content
		}
	}
	meta.Tags = append(meta.Tags, tag)
	return writeFrontMatter(body, meta)
}

type toolbarItem struct{ label, action string }

type copyResultMsg struct {
	err     error
	content string
}
type selectionModeMsg struct{}
type statusClearMsg struct{ id uint64 }

type cursorPos struct{ row, col int }

type autoSaveMsg struct{}

type command struct {
	name   string
	action func()
}

type helpRow struct {
	keys string
	desc string
}

type helpGroup struct {
	title string
	rows  []helpRow
}

var helpGroupsData = []helpGroup{
	{
		title: "Navigate",
		rows: []helpRow{
			{"↑/↓ or J/K", "Select item"},
			{"←/→ or H/L", "Collapse / expand"},
			{"Enter", "Open note"},
			{"Ctrl+O", "Quick open note"},
			{"Tab", "Switch panel"},
			{"t", "Toggle tree visibility"},
			{"Alt+← / Alt+→", "Back / forward history"},
			{"F / B", "Page preview down / up"},
			{"f", "Find in note"},
		},
	},
	{
		title: "Notes",
		rows: []helpRow{
			{"n / Ctrl+N", "New note"},
			{"N / Ctrl+D", "New folder"},
			{"F2 or R", "Rename"},
			{"Delete or X", "Delete"},
			{"*", "Pin / unpin note"},
			{"Ctrl+E", "Edit / preview"},
			{"Ctrl+S", "Save"},
			{"s", "Save (preview)"},
			{"Ctrl+Z", "Undo"},
			{"Ctrl+Shift+Z / Ctrl+Y", "Redo"},
			{"Ctrl+Shift+T", "Edit tags"},
			{"#", "Filter by tag"},
			{"Ctrl+Shift+P", "Command palette"},
		},
	},
	{
		title: "Copy",
		rows: []helpRow{
			{"Ctrl+C", "Copy text"},
			{"y", "Copy text (preview)"},
			{"Ctrl+L", "Copy current line (edit)"},
			{"Ctrl+G", "Select terminal text"},
		},
	},
	{
		title: "Search",
		rows: []helpRow{
			{"Ctrl+F", "Search note"},
			{"Ctrl+Shift+O", "Search everywhere"},
			{"Alt+G", "Go to line"},
		},
	},
	{
		title: "Batch & export",
		rows: []helpRow{
			{"Space", "Toggle multi-select"},
			{"Ctrl+A", "Select all"},
			{"Ctrl+Shift+A", "Clear selection"},
			{"Ctrl+Shift+E", "Export"},
			{"Alt+H", "Export note as HTML"},
		},
	},
	{
		title: "App",
		rows: []helpRow{
			{"Ctrl+Shift+F", "Focus mode"},
			{"Ctrl+R", "Refresh"},
			{"Ctrl+Q", "Quit"},
			{"?", "Help"},
			{"Esc", "Close / cancel"},
		},
	},
}

// SessionState is the persisted workspace state restored on startup.
type SessionState struct {
	CurrentPath string          `json:"currentPath"`
	CursorRow   int             `json:"cursorRow"`
	CursorCol   int             `json:"cursorCol"`
	Mode        string          `json:"mode"`
	Expanded    map[string]bool `json:"expanded"`
	TreeOffset  int             `json:"treeOffset"`
	PreviewOff  int             `json:"previewOff"`
	ActivePane  string          `json:"activePane"`
	Recent      []string        `json:"recent"`
}

type globalSearchResult struct {
	path    string
	title   string
	snippet string
	lineNum int
}

type quickOpenResult struct {
	path  string
	title string
	dir   string
}

type globalSearchMsg struct {
	query string
}

type matchPos struct {
	line  int
	start int
	end   int
}

type editorSel struct {
	anchor cursorPos
	end    cursorPos
}

type editSnapshot struct {
	content string
	row     int
	col     int
}

type editRecord struct {
	before editSnapshot
	after  editSnapshot
}

// Model is the TN Bubble Tea application.
type Model struct {
	store *storage.Store
	images *imageStore

	tree       []*storage.Node
	flat       []flatNode
	expanded   map[string]bool
	selected   int
	treeOffset int
	active     pane
	mode       mode
	beforeHelp mode

	currentPath     string
	original        string
	editor          textarea.Model
	preview         viewport.Model
	renderedPlain   string
	renderedContent string
	editSel         *editorSel
	undoStack       []editRecord
	redoStack       []editRecord
	input           textinput.Model
	promptKind      promptKind
	beforePrompt    mode
	exportPath      bool
	exportCopy      bool
	exportHTML      bool

	recent []string

	searchQuery   string
	searchMatches []matchPos
	searchIndex   int

	width, height    int
	treeWidth        int
	bodyHeight       int
	compact          bool
	treeVisible      bool
	selecting        bool
	contentDragging  bool
	contentSelAnchor int
	contentSelEnd    int
	copier           func(string) error
	pending          tea.Cmd

	focusing            bool
	sessionPath         string
	beforeGlobalSearch  mode
	globalSearchQuery   string
	globalSearchResults []globalSearchResult
	globalSearchIndex   int

	beforeQuickOpen  mode
	quickOpenQuery   string
	quickOpenResults []quickOpenResult
	quickOpenIndex   int

	tagFilter  string
	nodeTags   map[string][]string
	nodePinned map[string]bool

	renderer      *glamour.TermRenderer
	rendererWidth int

	status     string
	statusErr  bool
	statusOK   bool
	statusID   uint64
	statusCmd  tea.Cmd
	confirm    string
	confirmDir bool

	selectedItems map[string]bool
	confirmCount  int
	batchExport   bool
	batchTag      bool
	sortMode      sortMode

	lastEditTime time.Time

	helpHintView viewport.Model
	helpHintQ    string

	commandBeforeMode   mode
	commandBeforeActive pane
	commandIndex        int
	commandQuery        string

	webdavInputStep int
	webdavConfig    WebDAVConfig

	history      []string
	historyIndex int
}

func New(store *storage.Store) Model {
	editor := textarea.New()
	editor.Placeholder = "Write Markdown here…"
	editor.ShowLineNumbers = true
	editor.CharLimit = 0
	editor.SetWidth(60)
	editor.SetHeight(20)
	editor.FocusedStyle.Base = lipgloss.NewStyle().Foreground(text).Background(bg)
	editor.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(selection)
	editor.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(accent).Background(surface)
	editor.FocusedStyle.LineNumber = lipgloss.NewStyle().Foreground(muted).Background(bg)
	editor.FocusedStyle.Text = lipgloss.NewStyle().Foreground(text).Background(bg)
	editor.BlurredStyle = editor.FocusedStyle

	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 120
	input.PromptStyle = brandSty
	input.TextStyle = lipgloss.NewStyle().Foreground(text)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(accent)

	m := Model{
		store:         store,
		images:        newImageStore(store.Root),
		expanded:      make(map[string]bool),
		nodeTags:      make(map[string][]string),
		nodePinned:    make(map[string]bool),
		selectedItems: make(map[string]bool),
		recent:        make([]string, 0, 20),
		active:        treePane,
		treeVisible:   true,
		mode:          modeNormal,
		editor:        editor,
		preview:       viewport.New(60, 20),
		input:         input,
		copier:        copyText,
		status:        "Ready",
		sessionPath:   defaultSessionPath(),
		helpHintView:  viewport.New(60, 20),
	}
	m.preview.MouseWheelEnabled = true
	m.preview.MouseWheelDelta = 2
	// Preview paging: Space page down, B page up (Shift+F / Shift+B also work).
	// Lowercase f stays bound to find-in-note via globalKey.
	m.preview.KeyMap.PageDown.SetKeys("pgdown", " ", "F")
	m.preview.KeyMap.PageUp.SetKeys("pgup", "B", "b")
	m.helpHintView.MouseWheelEnabled = true
	m.helpHintView.MouseWheelDelta = 2
	m.refresh("")
	m = m.restoreSession()
	return m
}

func defaultSessionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tn", "session.json")
}

func (m Model) restoreSession() Model {
	if m.sessionPath == "" {
		return m
	}
	data, err := os.ReadFile(m.sessionPath)
	if err != nil {
		return m
	}
	var s SessionState
	if err := json.Unmarshal(data, &s); err != nil {
		return m
	}
	if s.Expanded != nil {
		m.expanded = s.Expanded
		m.rebuildFlat()
	}
	m.recent = s.Recent
	if m.recent == nil {
		m.recent = make([]string, 0, 20)
	}
	m.treeOffset = max(0, s.TreeOffset)
	m.active = treePane
	if s.ActivePane == "content" {
		m.active = contentPane
	}
	if s.CurrentPath != "" {
		content, err := m.store.Read(s.CurrentPath)
		if err == nil {
			m.currentPath = s.CurrentPath
			m.original = content
			m.editor.SetValue(content)
			m.undoStack = nil
			m.redoStack = nil
			m.editSel = nil
			m.selectPath(s.CurrentPath)
			m.setEditorBackground(bg)
			m.renderMarkdown()
		}
	}
	if s.Mode == "edit" && m.currentPath != "" {
		m.mode = modeEdit
		m.active = contentPane
		m.lastEditTime = time.Now()
		m.setEditorBackground(surface)
		m.editor.Focus()
		m.setCursor(s.CursorRow, s.CursorCol)
	}
	if s.PreviewOff > 0 && m.renderedContent != "" {
		m.preview.SetYOffset(s.PreviewOff)
	}
	return m
}

func (m Model) saveSession() {
	if m.sessionPath == "" {
		return
	}
	s := SessionState{
		CurrentPath: m.currentPath,
		CursorRow:   m.cursorPos().row,
		CursorCol:   m.cursorPos().col,
		Mode:        "preview",
		Expanded:    m.expanded,
		TreeOffset:  m.treeOffset,
		PreviewOff:  m.preview.YOffset,
		ActivePane:  "tree",
		Recent:      m.recent,
	}
	if m.mode == modeEdit {
		s.Mode = "edit"
	}
	if m.active == contentPane {
		s.ActivePane = "content"
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(m.sessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(m.sessionPath, data, 0o600)
}

func (m *Model) setCursor(row, col int) {
	m.gotoLineEdit(row)
	m.editor.SetCursor(col)
}

func (m Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, autoSaveCmd()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.MouseMsg:
		if m.selecting {
			return m, nil
		}
		if !m.focusing && (m.mode == modeNormal || m.mode == modeEdit) {
			m.handleMouse(tea.MouseEvent(msg))
		}
		return m, m.takePending()
	case tea.KeyMsg:
		key := msg.String()
		if m.selecting {
			m.selecting = false
			m.setStatus("Mouse restored", false)
			return m, tea.EnableMouseCellMotion
		}
		if key == "ctrl+c" && (m.mode == modeNormal || m.mode == modeEdit) {
			m.copyCurrent()
			return m, m.takePending()
		}
		if m.mode == modePrompt {
			return m.updatePrompt(msg)
		}
		if m.mode == modeConfirm {
			return m.updateConfirm(key)
		}
		if m.mode == modeHelp {
			return m.updateHelp(msg)
		}
		if m.mode == modeSearch {
			return m.updateSearch(msg)
		}
		if m.mode == modeSearchGlobal {
			return m.updateGlobalSearch(msg)
		}
		if m.mode == modeQuickOpen {
			return m.updateQuickOpen(msg)
		}
		if m.mode == modeExport {
			return m.updateExport(msg)
		}
		if m.mode == modeTag {
			return m.updateTagEdit(msg)
		}
		if m.mode == modeTagFilter {
			return m.updateTagFilter(msg)
		}
		if m.mode == modeCommand {
			return m.updateCommand(msg)
		}
		if m.mode == modeWebdavConfig {
			return m.updateWebdavConfig(msg)
		}

		if handled, quit := m.globalKey(key); handled {
			if quit {
				return m, tea.Quit
			}
			return m, m.takePending()
		}

		if m.mode == modeEdit {
			m.lastEditTime = time.Now()
			before := m.editor.Value()
			beforePos := m.cursorPos()
			if plain, ok := editSelectionKey(msg); ok {
				if m.editSel == nil {
					pos := m.cursorPos()
					m.editSel = &editorSel{anchor: pos, end: pos}
				}
				m.editor, cmd = m.editor.Update(plain)
				if m.editSel != nil {
					m.editSel.end = m.cursorPos()
				}
			} else {
				if msg.Type == tea.KeyEnter {
					if m.handleEditEnter(before, beforePos) {
						m.editSel = nil
						return m, nil
					}
				}
				// Handle Ctrl+V for image paste in edit mode.
				if msg.Type == tea.KeyCtrlV {
					if ref, ok := m.tryPasteImage(); ok {
						m.insertImageRef(ref)
						m.editSel = nil
						m.recordEdit(before, m.editor.Value(), beforePos, m.cursorPos())
						return m, cmd
					}
				}
				m.editSel = nil
				m.editor, cmd = m.editor.Update(msg)
			}
			m.recordEdit(before, m.editor.Value(), beforePos, m.cursorPos())
			return m, cmd
		}
		if m.active == treePane {
			m.treeKey(key)
			return m, m.takePending()
		}
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	case copyResultMsg:
		if msg.err != nil {
			m.exportCopy = false
			m.flashStatus("Copy failed: "+msg.err.Error()+" · Select text manually and use terminal copy", true, 3*time.Second)
		} else if m.exportCopy {
			m.exportCopy = false
			m.flashStatus("✓ Copied to clipboard", false, 2*time.Second)
		} else {
			m.flashStatus(copyFeedback(msg.content), false, 2*time.Second)
		}
		return m, m.takePending()
	case statusClearMsg:
		if msg.id == m.statusID {
			m.status, m.statusErr, m.statusOK = "", false, false
		}
		return m, nil
	case selectionModeMsg:
		m.selecting = true
		return m, nil
	case globalSearchMsg:
		if msg.query == m.globalSearchQuery {
			m.runGlobalSearch(msg.query)
		}
		return m, nil
	case autoSaveMsg:
		if m.mode == modeEdit && m.dirty() && time.Since(m.lastEditTime) >= 2*time.Second {
			m.autoSave()
		}
		return m, tea.Batch(autoSaveCmd(), m.takePending())
	}

	if m.mode == modeEdit {
		m.editor, cmd = m.editor.Update(msg)
	} else if m.active == contentPane {
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m *Model) globalKey(key string) (handled, quit bool) {
	if m.mode == modeEdit && len([]rune(key)) == 1 {
		return false, false
	}
	switch key {
	case "ctrl+q":
		if m.dirty() {
			m.setStatus("Save or discard changes before quitting", true)
			return true, false
		}
		m.saveSession()
		return true, true
	case "ctrl+s":
		m.save()
		return true, false
	case "ctrl+z":
		if m.mode == modeEdit {
			m.undo()
			return true, false
		}
		return false, false
	case "ctrl+shift+z":
		if m.mode == modeEdit {
			m.redo()
			return true, false
		}
		return false, false
	case "ctrl+y":
		if m.mode == modeEdit {
			m.redo()
			return true, false
		}
		m.copyCurrent()
		return true, false
	case "ctrl+shift+e":
		if m.mode == modeEdit {
			return false, false
		}
		if m.selectedCount() > 0 {
			m.startBatchExport()
			return true, false
		}
		if m.currentPath == "" {
			m.setStatus("Open a note first", true)
			return true, false
		}
		m.startExport()
		return true, false
	case "alt+h":
		if m.mode == modeEdit {
			return false, false
		}
		if m.currentPath == "" {
			m.setStatus("Open a note first", true)
			return true, false
		}
		m.startHTMLExport()
		return true, false
	case "ctrl+n":
		if m.mode == modeEdit {
			return false, false
		}
		m.startPrompt(promptNote)
		return true, false
	case "ctrl+d":
		if m.mode == modeEdit {
			return false, false
		}
		m.startPrompt(promptDir)
		return true, false
	case "f2":
		if m.mode == modeEdit {
			return false, false
		}
		m.startPrompt(promptRename)
		return true, false
	case "delete":
		if m.mode == modeEdit {
			return false, false
		}
		m.startDelete()
		return true, false
	case "ctrl+backspace":
		if m.mode == modeEdit {
			return false, false
		}
		m.startDelete()
		return true, false
	case "x":
		m.startDelete()
		return true, false
	case "r":
		m.startPrompt(promptRename)
		return true, false
	case "n":
		m.startPrompt(promptNote)
		return true, false
	case "N":
		m.startPrompt(promptDir)
		return true, false
	case "e":
		m.toggleEdit()
		return true, false
	case "s":
		m.save()
		return true, false
	case "t":
		m.toggleTree()
		return true, false
	case "y":
		m.copyCurrent()
		return true, false
	case "q":
		if m.dirty() {
			m.setStatus("Save or discard changes before quitting", true)
			return true, false
		}
		m.saveSession()
		return true, true
	case "alt+left":
		if m.mode == modeEdit {
			return false, false
		}
		m.goBack()
		return true, false
	case "alt+right":
		if m.mode == modeEdit {
			return false, false
		}
		m.goForward()
		return true, false
	case "*":
		m.togglePinned()
		return true, false
	case "ctrl+shift+o":
		m.startGlobalSearch()
		return true, false
	case "ctrl+o":
		m.startQuickOpen()
		return true, false
	case "ctrl+shift+f":
		m.toggleFocus()
		return true, false
	case "ctrl+shift+t":
		if m.selectedCount() > 0 {
			m.startBatchTag()
			return true, false
		}
		if m.mode != modeEdit {
			m.setStatus("Open a note and press Ctrl+E to edit first", true)
			return true, false
		}
		m.startTagEdit()
		return true, false
	case "ctrl+r":
		m.refresh(m.selectedPath())
		m.flashStatus("Notebook refreshed", false, 2*time.Second)
		return true, false
	case "ctrl+f", "f":
		if m.mode == modeEdit {
			return false, false
		}
		if m.currentPath == "" {
			m.setStatus("Open a note first", true)
			return true, false
		}
		m.startSearch()
		return true, false
	case "alt+g":
		m.startGotoLine()
		return true, false
	case "ctrl+l":
		if m.mode == modeEdit {
			m.copyCurrentLine()
			return true, false
		}
		return false, false
	case "ctrl+g":
		if m.mode == modeEdit {
			return false, false
		}
		m.enterSelectionMode()
		return true, false
	case "ctrl+e":
		if m.mode == modeEdit {
			return false, false
		}
		m.toggleEdit()
		return true, false
	case "tab":
		if m.mode == modeEdit {
			return false, false
		}
		m.togglePane()
		return true, false
	case "esc":
		if m.focusing {
			m.exitFocus()
		} else if m.mode == modeEdit {
			m.leaveEdit()
		} else {
			m.active = treePane
		}
		return true, false
	case "#":
		m.startTagFilter()
		return true, false
	case "ctrl+shift+p":
		m.startCommand()
		return true, false
	case "ctrl+shift+s":
		m.startWebdavConfig()
		return true, false
	case "ctrl+shift+w":
		m.syncWebdavNow()
		return true, false
	case "?":
		m.startHelp()
		return true, false
	}
	return false, false
}

func (m *Model) treeKey(key string) {
	switch key {
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "home", "g":
		m.selected = 0
		m.ensureSelectionVisible()
	case "end", "G":
		m.selected = max(0, len(m.flat)-1)
		m.ensureSelectionVisible()
	case "pgup":
		m.moveSelection(-m.treeRows())
	case "pgdown":
		m.moveSelection(m.treeRows())
	case "right", "l", "enter":
		m.activateSelected()
	case "left", "h":
		m.collapseOrParent()
	case " ":
		m.toggleSelect()
	case "ctrl+a":
		m.selectAllVisible()
	case "ctrl+shift+a":
		m.clearSelection()
	}
}

func (m *Model) toggleSelect() {
	if len(m.flat) == 0 {
		return
	}
	n := m.flat[m.selected].node
	if n.IsDir {
		return
	}
	if m.selectedItems[n.RelPath] {
		delete(m.selectedItems, n.RelPath)
	} else {
		m.selectedItems[n.RelPath] = true
	}
}

func (m *Model) selectAllVisible() {
	for _, item := range m.flat {
		if !item.node.IsDir {
			m.selectedItems[item.node.RelPath] = true
		}
	}
}

func (m *Model) clearSelection() {
	m.selectedItems = make(map[string]bool)
}

func (m *Model) selectedCount() int {
	count := 0
	for _, item := range m.flat {
		if !item.node.IsDir && m.selectedItems[item.node.RelPath] {
			count++
		}
	}
	return count
}

func (m *Model) handleMouse(msg tea.MouseEvent) {
	if m.contentDragging {
		// Track a content-pane drag selection: motion updates the selection
		// end, release copies the selected text and clears the selection.
		switch msg.Action {
		case tea.MouseActionMotion:
			if off := m.contentOffsetAt(msg.X, msg.Y); off >= 0 {
				m.contentSelEnd = off
			}
		case tea.MouseActionRelease:
			if off := m.contentOffsetAt(msg.X, msg.Y); off >= 0 {
				m.contentSelEnd = off
			}
			m.finishContentDrag()
		}
		return
	}
	if msg.Action != tea.MouseActionPress && !msg.IsWheel() {
		return
	}
	if msg.Y == 0 && msg.Button == tea.MouseButtonLeft {
		if p, ok := m.headerTabAt(msg.X); ok {
			if p == treePane {
				m.switchToTree()
			} else {
				m.switchToContent()
			}
		}
		return
	}
	if msg.Y == m.height-1 && msg.Button == tea.MouseButtonLeft {
		if action := m.footerActionAt(msg.X); action != "" {
			m.runAction(action)
		}
		return
	}
	if msg.Y < 1 || msg.Y >= 1+m.bodyHeight {
		return
	}

	inTree := !m.compact && m.treeVisible && msg.X < m.treeWidth
	if m.compact {
		inTree = m.active == treePane && m.treeVisible
	}
	if inTree {
		m.switchToTree()
		if msg.Button == tea.MouseButtonWheelUp {
			m.treeOffset = max(0, m.treeOffset-1)
			return
		}
		if msg.Button == tea.MouseButtonWheelDown {
			m.treeOffset = min(max(0, len(m.flat)-m.treeRows()), m.treeOffset+1)
			return
		}
		if msg.Button == tea.MouseButtonLeft {
			row := msg.Y - 2 + m.treeOffset
			if row >= m.treeOffset && row < min(len(m.flat), m.treeOffset+m.treeRows()) {
				m.selected = row
				if m.flat[row].node.IsDir {
					m.activateSelected()
				} else {
					m.openSelectedNote()
					if m.mode == modeEdit {
						m.switchToContent()
					}
				}
			}
		}
		return
	}

	m.switchToContent()
	if m.mode == modeNormal && msg.Button == tea.MouseButtonLeft {
		if off := m.contentOffsetAt(msg.X, msg.Y); off >= 0 {
			m.contentSelAnchor = off
			m.contentSelEnd = off
			m.contentDragging = true
			return
		}
	}
	if m.mode == modeNormal && msg.IsWheel() {
		updated, _ := m.preview.Update(tea.MouseMsg(msg))
		m.preview = updated
	}
}

// contentOffsetAt maps a screen position to a rune offset in the rendered
// preview text (m.renderedPlain), or returns -1 when the position is not over
// selectable preview text. Only meaningful in preview mode with a note open;
// the rendered content and plain text share a 1:1 line structure, and the
// preview viewport shows lines starting at its YOffset.
func (m Model) contentOffsetAt(x, y int) int {
	if m.mode != modeNormal || m.currentPath == "" || m.renderedPlain == "" {
		return -1
	}
	left := 1
	if !m.compact && m.treeVisible {
		left = m.treeWidth + 1
	}
	if x < left || x >= m.width-1 || y < 2 || y >= m.bodyHeight {
		return -1
	}
	// The panel's top border is at y=1; inside it sit the metadata lines,
	// then the preview viewport rows.
	metaLines := 1
	if len(m.nodeTags[m.currentPath]) > 0 {
		metaLines = 2
	}
	row := y - 2 - metaLines
	if row < 0 || row >= m.preview.Height {
		return -1
	}
	line := m.preview.YOffset + row
	lines := strings.Split(m.renderedPlain, "\n")
	if line >= len(lines) {
		return -1
	}
	offset := 0
	for _, l := range lines[:line] {
		offset += utf8.RuneCountInString(l) + 1
	}
	col := x - left
	if col > len([]rune(lines[line])) {
		col = len([]rune(lines[line]))
	}
	return offset + col
}

// finishContentDrag ends a content-pane drag selection: it copies the
// selected span of the rendered preview text and clears the selection state.
func (m *Model) finishContentDrag() {
	m.contentDragging = false
	start, end := m.contentSelAnchor, m.contentSelEnd
	m.contentSelAnchor, m.contentSelEnd = 0, 0
	if start > end {
		start, end = end, start
	}
	total := utf8.RuneCountInString(m.renderedPlain)
	if end > total {
		end = total
	}
	if start < 0 || end < 0 || start >= end {
		return
	}
	m.startCopy(string([]rune(m.renderedPlain)[start:end]))
}

func (m *Model) runAction(action string) {
	switch action {
	case "note":
		m.startPrompt(promptNote)
	case "tagfilter":
		m.startTagFilter()
	case "folder":
		m.startPrompt(promptDir)
	case "rename":
		m.startPrompt(promptRename)
	case "delete":
		m.startDelete()
	case "quit":
		if m.dirty() {
			m.setStatus("Save or discard changes before quitting", true)
		} else {
			m.saveSession()
			m.pending = tea.Quit
		}
	case "edit":
		m.toggleEdit()
	case "copy":
		m.copyCurrent()
	case "find":
		m.startSearch()
	case "select":
		m.enterSelectionMode()
	case "save":
		m.save()
	case "pane":
		m.togglePane()
	case "tree":
		m.toggleTree()
	case "help":
		m.startHelp()
	}
}

func (m *Model) enterSelectionMode() {
	if m.mode == modeEdit {
		m.leaveEdit()
	}
	m.active = contentPane
	m.pending = tea.Sequence(tea.DisableMouse, func() tea.Msg { return selectionModeMsg{} })
	m.setStatus("Select text now · your terminal may auto-copy · press any key to return", false)
}

func (m *Model) copyCurrent() {
	if m.currentPath == "" {
		m.flashStatus("Open a note first", true, 2*time.Second)
		return
	}
	var content string
	if m.mode == modeEdit {
		if m.editSel != nil {
			content = m.selectionText()
		} else {
			content = m.currentLineText()
		}
	} else {
		content = m.renderedPlain
	}
	m.startCopy(content)
}

func (m *Model) copyCurrentLine() {
	if m.currentPath == "" {
		m.flashStatus("Open a note first", true, 2*time.Second)
		return
	}
	if m.mode != modeEdit {
		m.flashStatus("Ctrl+L works in edit mode", true, 2*time.Second)
		return
	}
	m.startCopy(m.currentLineText())
}

func (m *Model) startCopy(content string) {
	copier := m.copier
	if copier == nil {
		copier = copyText
	}
	m.setStatus("Copying…", false)
	m.pending = copyCmd(copier, content)
}

func (m *Model) takePending() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2)
	if m.pending != nil {
		cmds = append(cmds, m.pending)
	}
	if m.statusCmd != nil {
		cmds = append(cmds, m.statusCmd)
	}
	m.pending = nil
	m.statusCmd = nil
	return tea.Batch(cmds...)
}

func copyText(content string) error {
	if err := clipboard.WriteAll(content); err == nil {
		return nil
	}
	// OSC 52 works over SSH and in many modern terminals when no OS helper exists.
	_, err := os.Stdout.WriteString(termansi.SetSystemClipboard(content))
	return err
}

func copyCmd(copier func(string) error, content string) tea.Cmd {
	return func() tea.Msg { return copyResultMsg{err: copier(content), content: content} }
}

func copyFeedback(content string) string {
	chars := len([]rune(content))
	if chars == 0 {
		return "✓ Copied empty note"
	}
	lines := strings.Count(content, "\n") + 1
	if strings.HasSuffix(content, "\n") {
		lines--
	}
	display := strings.Join(strings.Fields(content), " ")
	words := len(strings.Fields(content))
	switch {
	case chars <= 40:
		if lines > 1 {
			return fmt.Sprintf("✓ Copied %d lines: %s…", lines, truncate(display, 36))
		}
		return "✓ Copied " + display
	case chars <= 120:
		if lines > 1 {
			return fmt.Sprintf("✓ Copied %d lines · %d chars: %s…", lines, chars, truncate(display, 36))
		}
		return fmt.Sprintf("✓ Copied %d chars: %s…", chars, truncate(display, 36))
	default:
		wordLabel := "words"
		if words == 1 {
			wordLabel = "word"
		}
		if lines > 1 {
			return fmt.Sprintf("✓ Copied %d lines · %d chars · %d %s", lines, chars, words, wordLabel)
		}
		return fmt.Sprintf("✓ Copied %d chars · %d %s", chars, words, wordLabel)
	}
}

func (m *Model) startExport() {
	m.mode = modeExport
	m.exportPath = false
	m.exportCopy = false
	m.exportHTML = false
	m.statusErr = false
	m.status = ""
	m.input.Prompt = "Export to: "
	m.input.Placeholder = "filename or path"
	m.input.SetValue(m.defaultExportPath())
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Blur()
}

func (m *Model) startHTMLExport() {
	m.mode = modeExport
	m.exportPath = true
	m.exportCopy = false
	m.exportHTML = true
	m.statusErr = false
	m.status = ""
	m.input.Prompt = "Export HTML to: "
	m.input.Placeholder = "filename or path"
	m.input.SetValue(m.defaultHTMLPath())
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
}

func (m *Model) startBatchExport() {
	m.mode = modeExport
	m.exportPath = false
	m.batchExport = true
	m.exportCopy = false
	m.exportHTML = false
	m.statusErr = false
	m.status = ""
	m.input.Blur()
}

func (m Model) updateExport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.exportPath {
		switch msg.String() {
		case "esc":
			m.mode = modeNormal
			m.batchExport = false
			m.input.Blur()
			return m, nil
		}
		if m.batchExport {
			switch msg.String() {
			case "1":
				m.exportHTML = false
				m.exportPath = true
				m.statusErr = false
				m.status = ""
				m.input.Prompt = m.exportPrompt()
				m.input.Placeholder = "directory path"
				m.input.SetValue("")
				m.input.Width = max(20, min(60, m.width-12))
				m.input.Focus()
				return m, nil
			case "2":
				m.exportHTML = true
				m.exportPath = true
				m.statusErr = false
				m.status = ""
				m.input.Prompt = m.exportPrompt()
				m.input.Placeholder = "directory path"
				m.input.SetValue("")
				m.input.Width = max(20, min(60, m.width-12))
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "1":
			m.doExportCopy()
			return m, m.takePending()
		case "2":
			m.exportHTML = false
			m.exportPath = true
			m.statusErr = false
			m.status = ""
			m.input.SetValue(m.defaultExportPath())
			m.input.Prompt = m.exportPrompt()
			m.input.Focus()
			return m, nil
		case "3":
			m.exportHTML = true
			m.exportPath = true
			m.statusErr = false
			m.status = ""
			m.input.SetValue(m.defaultHTMLPath())
			m.input.Prompt = m.exportPrompt()
			m.input.Focus()
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.exportPath = false
		m.batchExport = false
		m.exportHTML = false
		m.input.Blur()
		return m, nil
	case "enter":
		if m.batchExport {
			m.performBatchExport()
		} else {
			m.performSaveAs()
		}
		if m.mode == modeExport {
			return m, nil
		}
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) doExportCopy() {
	m.mode = modeNormal
	m.exportPath = false
	m.input.Blur()
	m.exportCopy = true
	m.startCopy(m.renderedPlain)
}

func (m *Model) performSaveAs() {
	raw := strings.TrimSpace(m.input.Value())
	if raw == "" {
		m.setStatus("Enter a path to export", true)
		return
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.exportDir(), path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.setStatus("Export failed: "+err.Error(), true)
		return
	}
	if m.exportHTML {
		htmlContent, err := buildHTMLExport(m.currentPath, m.editor.Value())
		if err != nil {
			m.setStatus("Export failed: "+err.Error(), true)
			return
		}
		if err := os.WriteFile(path, []byte(htmlContent), 0o644); err != nil {
			m.setStatus("Export failed: "+err.Error(), true)
			return
		}
		m.mode = modeNormal
		m.exportPath = false
		m.exportHTML = false
		m.input.Blur()
		m.flashStatus("✓ Exported HTML to "+path, false, 2*time.Second)
		return
	}
	if err := os.WriteFile(path, []byte(m.editor.Value()), 0o644); err != nil {
		m.setStatus("Export failed: "+err.Error(), true)
		return
	}
	m.mode = modeNormal
	m.exportPath = false
	m.exportHTML = false
	m.input.Blur()
	m.flashStatus("✓ Exported to "+path, false, 2*time.Second)
}

func (m *Model) performBatchExport() {
	raw := strings.TrimSpace(m.input.Value())
	if raw == "" {
		m.setStatus("Enter a directory to export to", true)
		return
	}
	dir := raw
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(m.store.Root, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.setStatus("Export failed: "+err.Error(), true)
		return
	}
	count := 0
	for _, item := range m.flat {
		path := item.node.RelPath
		if item.node.IsDir || !m.selectedItems[path] {
			continue
		}
		content, err := m.store.Read(path)
		if err != nil {
			continue
		}
		target := filepath.Join(dir, path)
		if m.exportHTML {
			content, err = buildHTMLExport(path, content)
			if err != nil {
				continue
			}
			target = filepath.Join(dir, strings.TrimSuffix(path, filepath.Ext(path))+".html")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			continue
		}
		count++
	}
	if count == 0 {
		m.setStatus("Export failed: no notes could be written", true)
		return
	}
	m.mode = modeNormal
	m.exportPath = false
	m.batchExport = false
	m.input.Blur()
	m.flashStatus(fmt.Sprintf("✓ Exported %d notes to %s", count, dir), false, 2*time.Second)
}

func (m Model) exportPrompt() string {
	if m.batchExport {
		label := "notes"
		if m.exportHTML {
			label = "notes as HTML"
		}
		return fmt.Sprintf("Export %d %s to: ", m.selectedCount(), label)
	}
	if m.exportHTML {
		return "Export HTML to: "
	}
	return "Export to: "
}

func (m Model) exportDir() string {
	if m.currentPath == "" {
		return m.store.Root
	}
	return filepath.Join(m.store.Root, filepath.Dir(m.currentPath))
}

func (m Model) defaultExportPath() string {
	if m.currentPath == "" {
		return ""
	}
	base := filepath.Base(m.currentPath)
	base = strings.TrimSuffix(base, filepath.Ext(base)) + ".md"
	return filepath.Join(m.exportDir(), base)
}

func (m Model) defaultHTMLPath() string {
	if m.currentPath == "" {
		return ""
	}
	base := filepath.Base(m.currentPath)
	base = strings.TrimSuffix(base, filepath.Ext(base)) + ".html"
	return filepath.Join(m.exportDir(), base)
}

// htmlExportData feeds the self-contained export template. Body is rendered
// Markdown that has already been sanitized by bluemonday.
type htmlExportData struct {
	Title   string
	Path    string
	Created string
	Tags    []string
	Body    template.HTML
}

var htmlExportPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
	--bg: #eef1f8;
	--paper: #ffffff;
	--ink: #1b1e2b;
	--muted: #68708c;
	--accent: #3f6fce;
	--rule: #dbe2f0;
	--code: #f0f3fa;
}
* { box-sizing: border-box; }
body {
	margin: 0;
	background: var(--bg);
	color: var(--ink);
	font: 15px/1.65 system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang SC", "Microsoft YaHei", sans-serif;
}
.page {
	max-width: 840px;
	margin: 0 auto;
	padding: 48px 28px 72px;
}
.paper {
	background: var(--paper);
	border: 1px solid var(--rule);
	border-radius: 8px;
	padding: 40px 44px;
	box-shadow: 0 10px 30px rgba(20, 22, 33, .08);
}
.head {
	border-bottom: 1px solid var(--rule);
	padding-bottom: 18px;
	margin-bottom: 24px;
}
h1.title { margin: 0 0 8px; font-size: 28px; line-height: 1.3; }
.meta { color: var(--muted); font-size: 13px; }
.tags { margin-top: 12px; }
.tag {
	display: inline-block;
	color: var(--accent);
	border: 1px solid var(--accent);
	border-radius: 99px;
	padding: 2px 10px;
	margin: 0 6px 4px 0;
	font-size: 12px;
}
main h1, main h2 { color: var(--accent); line-height: 1.35; }
main h1 { font-size: 24px; border-bottom: 2px solid var(--rule); padding-bottom: 6px; margin: 1.4em 0 .5em; }
main h2 { font-size: 20px; margin: 1.4em 0 .5em; }
main h3 { color: var(--ink); font-size: 17px; margin: 1.3em 0 .4em; }
main h4, main h5, main h6 { color: var(--muted); margin: 1.2em 0 .4em; }
main p { margin: .8em 0; }
main a { color: var(--accent); text-decoration: none; border-bottom: 1px solid var(--accent); }
main ul, main ol { padding-left: 1.4em; }
main li { margin: .25em 0; }
main code {
	background: var(--code);
	padding: .15em .4em;
	border-radius: 4px;
	font: 13px/1.5 "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
}
pre {
	background: var(--code);
	border: 1px solid var(--rule);
	border-radius: 6px;
	padding: 14px 16px;
	overflow-x: auto;
}
pre code { background: transparent; padding: 0; }
blockquote {
	margin: .8em 0;
	padding: .1em 1em;
	border-left: 3px solid var(--accent);
	color: var(--muted);
}
hr { border: none; border-top: 1px solid var(--rule); margin: 2em 0; }
table { border-collapse: collapse; width: 100%; margin: 1em 0; }
th, td { border: 1px solid var(--rule); padding: 7px 12px; text-align: left; }
th { background: var(--code); }
img { max-width: 100%; }
footer { margin-top: 28px; color: var(--muted); font-size: 12px; }
</style>
</head>
<body>
<div class="page">
<article class="paper">
<header class="head">
<h1 class="title">{{.Title}}</h1>
<div class="meta">{{.Path}}{{if .Created}} · created {{.Created}}{{end}}</div>
{{if .Tags}}<div class="tags">{{range .Tags}}<span class="tag">{{.}}</span>{{end}}</div>{{end}}
</header>
<main>
{{.Body}}
</main>
<footer>Exported from TN · Markdown</footer>
</article>
</div>
</body>
</html>
`

var htmlExportTemplate = template.Must(template.New("note").Parse(htmlExportPage))

// buildHTMLExport renders a note's Markdown body into a self-contained,
// styled HTML page. Front-matter metadata becomes the document title and
// tags, and raw HTML in the note is sanitized before being embedded.
func buildHTMLExport(path, content string) (string, error) {
	meta, body := parseFrontMatter(content)
	title := meta.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	rendered, err := renderMarkdownHTML(body)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := htmlExportTemplate.Execute(&buf, htmlExportData{
		Title:   title,
		Path:    path,
		Created: meta.Created,
		Tags:    meta.Tags,
		Body:    template.HTML(rendered),
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderMarkdownHTML(markdown string) (string, error) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var buf strings.Builder
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return "", err
	}
	return bluemonday.UGCPolicy().Sanitize(buf.String()), nil
}

func (m Model) exportDialogView() string {
	title := "Export"
	if m.batchExport {
		title = fmt.Sprintf("Export %d notes", m.selectedCount())
	}
	var body string
	if m.exportPath {
		format := "Markdown"
		if m.exportHTML {
			format = "HTML"
		}
		body = m.input.View() + "\n\n" + mutedSty.Render("Format: "+format) + "\n" + mutedSty.Render("Enter 导出  ·  Esc 取消")
		if m.statusErr {
			body += "\n" + errorSty.Render(m.status)
		}
	} else if m.batchExport {
		body = "1  Markdown\n2  HTML\n\n" + mutedSty.Render("Esc 取消")
	} else {
		body = "1  复制到剪贴板\n2  另存为\n3  导出 HTML\n\n" + mutedSty.Render("Esc 取消")
	}
	dialog := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(64, max(28, m.width-6))).Render(brandSty.Render(title) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func (m Model) cursorPos() cursorPos {
	li := m.editor.LineInfo()
	return cursorPos{row: m.editor.Line(), col: li.StartColumn + li.ColumnOffset}
}

func (m Model) currentLineText() string {
	lines := strings.Split(m.editor.Value(), "\n")
	row := m.editor.Line()
	if row < 0 || row >= len(lines) {
		return ""
	}
	return lines[row]
}

func (m Model) selectionText() string {
	if m.editSel == nil {
		return ""
	}
	start, end := m.editSel.anchor, m.editSel.end
	if start.row > end.row || (start.row == end.row && start.col > end.col) {
		start, end = end, start
	}
	lines := strings.Split(m.editor.Value(), "\n")
	if start.row >= len(lines) {
		return ""
	}
	if end.row >= len(lines) {
		end.row = len(lines) - 1
		end.col = len([]rune(lines[end.row]))
	}
	runeLines := make([][]rune, len(lines))
	for i, l := range lines {
		runeLines[i] = []rune(l)
	}
	if start.row == end.row {
		line := runeLines[start.row]
		return string(line[min(start.col, len(line)):min(end.col, len(line))])
	}
	var b strings.Builder
	b.WriteString(string(runeLines[start.row][min(start.col, len(runeLines[start.row])):]))
	for r := start.row + 1; r < end.row; r++ {
		b.WriteByte('\n')
		b.WriteString(string(runeLines[r]))
	}
	b.WriteByte('\n')
	b.WriteString(string(runeLines[end.row][:min(end.col, len(runeLines[end.row]))]))
	return b.String()
}

func (m *Model) recordEdit(before, after string, beforePos, afterPos cursorPos) {
	if before == after {
		return
	}
	m.undoStack = append(m.undoStack, editRecord{
		before: editSnapshot{content: before, row: beforePos.row, col: beforePos.col},
		after:  editSnapshot{content: after, row: afterPos.row, col: afterPos.col},
	})
	m.redoStack = m.redoStack[:0]
}

func (m *Model) applySnapshot(s editSnapshot) {
	m.editor.SetValue(s.content)
	m.editSel = nil
	m.setCursor(s.row, s.col)
}

func (m *Model) undo() {
	if m.mode != modeEdit || len(m.undoStack) == 0 {
		return
	}
	rec := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.redoStack = append(m.redoStack, rec)
	m.applySnapshot(rec.before)
}

func (m *Model) redo() {
	if m.mode != modeEdit || len(m.redoStack) == 0 {
		return
	}
	rec := m.redoStack[len(m.redoStack)-1]
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.undoStack = append(m.undoStack, rec)
	m.applySnapshot(rec.after)
}

func (m Model) undoable() bool { return len(m.undoStack) > 0 }
func (m Model) redoable() bool { return len(m.redoStack) > 0 }

func editSelectionKey(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	switch msg.Type {
	case tea.KeyShiftUp:
		return tea.KeyMsg{Type: tea.KeyUp}, true
	case tea.KeyShiftDown:
		return tea.KeyMsg{Type: tea.KeyDown}, true
	case tea.KeyShiftLeft:
		return tea.KeyMsg{Type: tea.KeyLeft}, true
	case tea.KeyShiftRight:
		return tea.KeyMsg{Type: tea.KeyRight}, true
	case tea.KeyShiftHome:
		return tea.KeyMsg{Type: tea.KeyHome}, true
	case tea.KeyShiftEnd:
		return tea.KeyMsg{Type: tea.KeyEnd}, true
	}
	return tea.KeyMsg{}, false
}

func (m *Model) togglePane() {
	if m.active == treePane {
		m.switchToContent()
	} else if m.treeVisible {
		m.switchToTree()
	}
}

func (m *Model) toggleTree() {
	m.treeVisible = !m.treeVisible
	if !m.treeVisible {
		m.active = contentPane
	}
	m.resize(m.width, m.height)
}

func (m *Model) switchToTree() {
	if m.mode == modeEdit && !m.dirty() {
		m.leaveEdit()
	}
	m.active = treePane
}

func (m *Model) switchToContent() {
	m.active = contentPane
	if m.mode == modeEdit {
		m.editor.Focus()
	}
}

func (m *Model) activateSelected() {
	if len(m.flat) == 0 {
		return
	}
	n := m.flat[m.selected].node
	if n.IsDir {
		m.expanded[n.RelPath] = !m.expanded[n.RelPath]
		selectedPath := n.RelPath
		m.rebuildFlat()
		m.selectPath(selectedPath)
		return
	}
	m.openSelectedNote()
	if m.currentPath == n.RelPath {
		m.switchToContent()
	}
}

func (m *Model) openSelectedNote() {
	if len(m.flat) == 0 || m.flat[m.selected].node.IsDir {
		return
	}
	path := m.flat[m.selected].node.RelPath
	if path == m.currentPath {
		return
	}
	if !m.openPath(path) {
		return
	}
	m.pushHistory(path)
	m.flashStatus("Opened "+path, false, 2*time.Second)
}

// openPath loads a note into the editor and preview without recording history.
// It returns false when the note cannot be opened or an active save fails.
func (m *Model) openPath(path string) bool {
	if path == "" {
		return false
	}
	if m.dirty() && !m.save() {
		return false
	}
	content, err := m.store.Read(path)
	if err != nil {
		m.setStatus(err.Error(), true)
		return false
	}
	m.currentPath = path
	m.original = content
	m.editor.SetValue(content)
	m.undoStack = nil
	m.redoStack = nil
	m.editSel = nil
	m.expandParents(path)
	m.rebuildFlat()
	m.selectPath(path)
	if m.mode == modeEdit {
		m.setEditorBackground(surface)
		m.editor.Focus()
	} else {
		m.setEditorBackground(bg)
		m.editor.Blur()
	}
	m.preview.GotoTop()
	m.renderMarkdown()
	return true
}

func (m *Model) pushHistory(path string) {
	m.rememberRecent(path)
	if len(m.history) > 0 && m.history[m.historyIndex] == path {
		return
	}
	if m.historyIndex < len(m.history)-1 {
		m.history = m.history[:m.historyIndex+1]
	}
	m.history = append(m.history, path)
	m.historyIndex = len(m.history) - 1
	if len(m.history) > 100 {
		m.history = m.history[1:]
		m.historyIndex--
	}
}

func (m *Model) goBack() {
	if m.historyIndex <= 0 {
		m.flashStatus("No earlier note", true, 2*time.Second)
		return
	}
	m.historyIndex--
	if m.dirty() && !m.save() {
		m.historyIndex++
		return
	}
	if !m.openPath(m.history[m.historyIndex]) {
		m.historyIndex++
	} else {
		m.rememberRecent(m.history[m.historyIndex])
	}
}

func (m *Model) goForward() {
	if m.historyIndex >= len(m.history)-1 {
		m.flashStatus("No newer note", true, 2*time.Second)
		return
	}
	m.historyIndex++
	if m.dirty() && !m.save() {
		m.historyIndex--
		return
	}
	if !m.openPath(m.history[m.historyIndex]) {
		m.historyIndex--
	} else {
		m.rememberRecent(m.history[m.historyIndex])
	}
}

func (m *Model) rememberRecent(path string) {
	if path == "" {
		return
	}
	out := make([]string, 0, len(m.recent)+1)
	out = append(out, path)
	for _, p := range m.recent {
		if p != path {
			out = append(out, p)
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	m.recent = out
}

func (m *Model) togglePinned() {
	if len(m.flat) == 0 {
		m.flashStatus("Select a note to pin", true, 2*time.Second)
		return
	}
	n := m.flat[m.selected].node
	if n.IsDir {
		m.flashStatus("Only notes can be pinned", true, 2*time.Second)
		return
	}
	path := n.RelPath
	content := ""
	if path == m.currentPath {
		content = m.editor.Value()
	} else {
		var err error
		content, err = m.store.Read(path)
		if err != nil {
			m.setStatus(err.Error(), true)
			return
		}
	}
	meta, body := parseFrontMatter(content)
	meta.Pinned = !meta.Pinned
	newContent := writeFrontMatter(body, meta)
	if err := m.store.Write(path, newContent); err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	if path == m.currentPath {
		m.editor.SetValue(newContent)
		m.original = newContent
		m.renderMarkdown()
	}
	m.nodePinned[path] = meta.Pinned
	m.rebuildFlat()
	m.selectPath(path)
	state := "unpinned"
	if meta.Pinned {
		state = "pinned"
	}
	m.flashStatus(state+" "+path, false, 2*time.Second)
}

func (m *Model) collapseOrParent() {
	if len(m.flat) == 0 {
		return
	}
	current := m.flat[m.selected]
	if current.node.IsDir && m.expanded[current.node.RelPath] {
		m.expanded[current.node.RelPath] = false
		m.rebuildFlat()
		m.selectPath(current.node.RelPath)
		return
	}
	parent := filepath.Dir(current.node.RelPath)
	if parent == "." {
		return
	}
	m.selectPath(parent)
}

func (m *Model) moveSelection(delta int) {
	if len(m.flat) == 0 {
		return
	}
	m.selected = max(0, min(len(m.flat)-1, m.selected+delta))
	m.ensureSelectionVisible()
}

func (m *Model) startPrompt(kind promptKind) {
	if kind == promptRename && len(m.flat) == 0 {
		m.flashStatus("Select an item to rename", true, 2*time.Second)
		return
	}
	m.promptKind = kind
	m.mode = modePrompt
	m.statusErr = false
	m.input.SetValue("")
	switch kind {
	case promptNote:
		m.input.Placeholder = "New note:"
	case promptDir:
		m.input.Placeholder = "New folder:"
	case promptRename:
		m.input.Placeholder = "New name:"
		m.input.SetValue(m.flat[m.selected].node.Name)
	}
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
}

func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.promptKind == promptGotoLine {
		return m.updateGotoLinePrompt(msg)
	}
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "enter":
		value := m.input.Value()
		if m.promptKind == promptRename && len(m.flat) > 0 && strings.TrimSpace(value) == m.flat[m.selected].node.Name {
			m.mode = modeNormal
			m.input.Blur()
			return m, nil
		}
		var path string
		var err error
		switch m.promptKind {
		case promptNote:
			path, err = m.store.CreateNote(m.selectedParent(), value)
		case promptDir:
			path, err = m.store.CreateDir(m.selectedParent(), value)
		case promptRename:
			path, err = m.renameSelected(value)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		if m.promptKind == promptNote && m.tagFilter != "" {
			if content, rerr := m.store.Read(path); rerr == nil {
				_ = m.store.Write(path, ensureTag(content, m.tagFilter))
			}
		}
		m.mode = modeNormal
		m.input.Blur()
		m.expandParents(path)
		m.refresh(path)
		if m.promptKind == promptNote {
			m.openSelectedNote()
			m.active = contentPane
			m.toggleEdit()
		} else if m.promptKind == promptDir {
			m.flashStatus("Created "+path, false, 2*time.Second)
		} else {
			m.flashStatus("Renamed to "+path, false, 2*time.Second)
		}
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) startTagEdit() {
	if m.currentPath == "" {
		m.setStatus("Open a note first", true)
		return
	}
	meta, _ := parseFrontMatter(m.editor.Value())
	m.input.SetValue(strings.Join(meta.Tags, ", "))
	m.input.Prompt = "Tags: "
	m.input.Placeholder = "tag1, tag2"
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
	m.mode = modeTag
	m.statusErr = false
	m.status = ""
}

func (m *Model) startBatchTag() {
	m.mode = modeTag
	m.batchTag = true
	m.input.SetValue("")
	m.input.Prompt = "Tags: "
	m.input.Placeholder = "tag1, tag2"
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
	m.statusErr = false
	m.status = ""
}

func (m Model) updateTagEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		m.input.Prompt = "› "
		if m.batchTag {
			m.batchTag = false
			m.mode = modeNormal
			m.active = treePane
			return m, nil
		}
		m.mode = modeEdit
		m.editor.Focus()
		return m, nil
	case "enter":
		wasBatch := m.batchTag
		if wasBatch {
			m.saveBatchTags(m.input.Value())
		} else {
			m.saveTags(m.input.Value())
		}
		if m.mode == modeTag {
			return m, nil
		}
		m.input.Blur()
		m.input.Prompt = "› "
		if !wasBatch {
			m.editor.Focus()
		}
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) saveBatchTags(raw string) {
	tags := parseTagsInput(raw)
	count := 0
	for _, item := range m.flat {
		path := item.node.RelPath
		if item.node.IsDir || !m.selectedItems[path] {
			continue
		}
		content, err := m.store.Read(path)
		if err != nil {
			continue
		}
		meta, body := parseFrontMatter(content)
		if meta.Title == "" {
			meta.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		if meta.Created == "" {
			meta.Created = time.Now().Format("2006-01-02")
		}
		meta.Tags = tags
		if err := m.store.Write(path, writeFrontMatter(body, meta)); err != nil {
			continue
		}
		m.nodeTags[path] = tags
		m.nodePinned[path] = meta.Pinned
		count++
	}
	if count == 0 {
		m.setStatus("No notes could be tagged", true)
		return
	}
	m.batchTag = false
	m.mode = modeNormal
	m.active = treePane
	if m.currentPath != "" && m.selectedItems[m.currentPath] {
		if content, err := m.store.Read(m.currentPath); err == nil {
			m.original = content
			m.editor.SetValue(content)
			m.renderMarkdown()
		}
	}
	m.rebuildFlat()
	m.flashStatus(fmt.Sprintf("✓ Tagged %d notes", count), false, 2*time.Second)
}

func (m *Model) saveTags(raw string) {
	if m.currentPath == "" {
		m.setStatus("Open a note first", true)
		return
	}
	meta, body := parseFrontMatter(m.editor.Value())
	if meta.Title == "" {
		meta.Title = strings.TrimSuffix(filepath.Base(m.currentPath), filepath.Ext(m.currentPath))
	}
	if meta.Created == "" {
		meta.Created = time.Now().Format("2006-01-02")
	}
	meta.Tags = parseTagsInput(raw)
	content := writeFrontMatter(body, meta)
	m.editor.SetValue(content)
	if err := m.store.Write(m.currentPath, content); err != nil {
		m.setStatus("Save tags failed: "+err.Error(), true)
		return
	}
	m.original = content
	m.redoStack = m.redoStack[:0]
	m.mode = modeEdit
	m.renderMarkdown()
	m.nodeTags[m.currentPath] = meta.Tags
	m.nodePinned[m.currentPath] = meta.Pinned
	m.rebuildFlat()
	m.flashStatus("Tags saved", false, 2*time.Second)
}

func (m Model) tagEditView() string {
	title := "Edit tags"
	if m.batchTag {
		title = fmt.Sprintf("Tag %d notes", m.selectedCount())
	}
	body := m.input.View() + "\n\n" + mutedSty.Render("Enter 保存 · Esc 取消")
	if m.statusErr {
		body += "\n" + errorSty.Render(m.status)
	}
	dialog := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(64, max(28, m.width-6))).Render(brandSty.Render(title) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func (m *Model) startTagFilter() {
	m.mode = modeTagFilter
	m.input.Prompt = "# "
	m.input.Placeholder = "tag name"
	m.input.SetValue(m.tagFilter)
	m.input.Width = max(10, min(50, m.width-12))
	m.input.Focus()
	m.statusErr = false
	m.status = ""
}

func (m Model) updateTagFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.tagFilter = ""
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		m.rebuildFlat()
		m.flashStatus("Tag filter cleared", false, 2*time.Second)
		return m, m.takePending()
	case "enter":
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		m.setStatus("Filtering by #"+strings.TrimSpace(m.tagFilter), false)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	filter := strings.TrimSpace(m.input.Value())
	if filter != m.tagFilter {
		m.tagFilter = filter
		m.rebuildFlat()
	}
	return m, cmd
}

func (m Model) tagFilterBarView() string {
	return m.composeBar(" # "+m.input.View(), mutedSty.Render("Type to filter by tag, Esc to cancel"))
}

func (m *Model) renameSelected(name string) (string, error) {
	oldPath := m.selectedPath()
	if oldPath == "" {
		return "", errors.New("select an item to rename")
	}
	newPath, err := m.store.Rename(oldPath, name)
	if err != nil {
		return "", err
	}
	if oldPath == m.currentPath {
		m.currentPath = newPath
	} else if strings.HasPrefix(m.currentPath, oldPath+string(filepath.Separator)) {
		m.currentPath = newPath + strings.TrimPrefix(m.currentPath, oldPath)
	}
	if m.expanded[oldPath] {
		delete(m.expanded, oldPath)
		m.expanded[newPath] = true
	}
	if m.selectedItems[oldPath] {
		delete(m.selectedItems, oldPath)
		m.selectedItems[newPath] = true
	}
	prefix := oldPath + string(filepath.Separator)
	for p := range m.selectedItems {
		if strings.HasPrefix(p, prefix) {
			delete(m.selectedItems, p)
			m.selectedItems[newPath+strings.TrimPrefix(p, oldPath)] = true
		}
	}
	return newPath, nil
}

func (m *Model) startDelete() {
	if len(m.flat) == 0 {
		m.flashStatus("Nothing selected", true, 2*time.Second)
		return
	}
	if m.selectedCount() > 0 {
		m.confirmCount = m.selectedCount()
		m.mode = modeConfirm
		return
	}
	n := m.flat[m.selected].node
	m.confirm = n.Name
	m.confirmDir = n.IsDir
	m.mode = modeConfirm
}

func (m Model) updateConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "Y", "enter":
		if m.confirmCount > 0 {
			m.batchDelete()
		} else {
			path := m.selectedPath()
			if m.currentPath == path || strings.HasPrefix(m.currentPath, path+string(filepath.Separator)) {
				m.currentPath = ""
				m.original = ""
				m.editor.SetValue("")
				m.undoStack = nil
				m.redoStack = nil
				m.preview.SetContent("")
			}
			if err := m.store.Delete(path); err != nil {
				m.flashStatus(err.Error(), true, 2*time.Second)
			} else {
				m.refresh("")
				m.flashStatus("Deleted "+path, false, 2*time.Second)
			}
		}
		m.confirmCount = 0
		m.mode = modeNormal
		// Clean up orphaned images after deletion.
		m.cleanupImages()
	case "n", "N", "esc":
		m.confirmCount = 0
		m.mode = modeNormal
		m.flashStatus("Delete cancelled", false, 2*time.Second)
	}
	return m, m.takePending()
}

// cleanupImages removes image files not referenced by any note.
func (m *Model) cleanupImages() {
	contents := m.allNoteContents()
	if removed, err := m.images.cleanupOrphanedImages(contents); err == nil && removed > 0 {
		m.flashStatus(fmt.Sprintf("Cleaned up %d unused images", removed), false, 2*time.Second)
	}
}

// allNoteContents returns the markdown content of every note in the notebook.
func (m *Model) allNoteContents() []string {
	var contents []string
	var walk func(nodes []*storage.Node)
	walk = func(nodes []*storage.Node) {
		for _, n := range nodes {
			if n.IsDir {
				walk(n.Children)
				continue
			}
			if content, err := m.store.Read(n.RelPath); err == nil {
				contents = append(contents, content)
			}
		}
	}
	walk(m.tree)
	return contents
}

func (m *Model) batchDelete() {
	count := 0
	for _, item := range m.flat {
		path := item.node.RelPath
		if item.node.IsDir || !m.selectedItems[path] {
			continue
		}
		if m.currentPath == path {
			m.currentPath = ""
			m.original = ""
			m.editor.SetValue("")
			m.undoStack = nil
			m.redoStack = nil
			m.preview.SetContent("")
		}
		if err := m.store.Delete(path); err != nil {
			continue
		}
		delete(m.selectedItems, path)
		count++
	}
	m.refresh("")
	if count > 0 {
		m.flashStatus(fmt.Sprintf("Deleted %d notes", count), false, 2*time.Second)
	} else {
		m.setStatus("Delete failed", true)
	}
}

func (m *Model) toggleEdit() {
	if m.currentPath == "" {
		m.setStatus("Open a note first", true)
		return
	}
	if m.mode == modeEdit {
		m.leaveEdit()
		return
	}
	m.mode = modeEdit
	m.active = contentPane
	m.editSel = nil
	m.lastEditTime = time.Now()
	m.setEditorBackground(surface)
	m.editor.Focus()
	m.setStatus("Editing "+m.currentPath, false)
}

func (m *Model) leaveEdit() {
	m.editor.Blur()
	m.mode = modeNormal
	m.editSel = nil
	m.setEditorBackground(bg)
	m.renderMarkdown()
	if m.dirty() {
		m.setStatus("Unsaved changes · Ctrl+S to save", false)
	} else {
		m.setStatus("Preview mode", false)
	}
}

func (m *Model) setEditorBackground(c lipgloss.Color) {
	base := lipgloss.NewStyle().Foreground(text).Background(c)
	m.editor.FocusedStyle.Base = base
	m.editor.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(selection)
	m.editor.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(accent).Background(surface)
	m.editor.FocusedStyle.LineNumber = lipgloss.NewStyle().Foreground(muted).Background(c)
	m.editor.FocusedStyle.Text = base
	m.editor.BlurredStyle = m.editor.FocusedStyle
}

func (m *Model) save() bool {
	if m.currentPath == "" || !m.dirty() {
		m.flashStatus("Nothing to save", false, 2*time.Second)
		return true
	}
	content := m.editor.Value()
	if err := m.store.Write(m.currentPath, content); err != nil {
		m.flashStatus("Save failed: "+err.Error(), true, 2*time.Second)
		return false
	}
	m.original = content
	m.redoStack = m.redoStack[:0]
	meta, _ := parseFrontMatter(content)
	m.nodeTags[m.currentPath] = meta.Tags
	m.nodePinned[m.currentPath] = meta.Pinned
	if m.tagFilter != "" {
		m.rebuildFlat()
	}
	m.renderMarkdown()
	m.flashStatus("✓ Saved "+m.currentPath, false, 2*time.Second)
	return true
}

func autoSaveCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return autoSaveMsg{} })
}

// autoSave writes changes silently on success and surfaces failures in red.
func (m *Model) autoSave() bool {
	if m.currentPath == "" || !m.dirty() {
		return true
	}
	content := m.editor.Value()
	if err := m.store.Write(m.currentPath, content); err != nil {
		m.flashStatus("Save failed: "+err.Error(), true, 2*time.Second)
		return false
	}
	m.original = content
	m.redoStack = m.redoStack[:0]
	meta, _ := parseFrontMatter(content)
	m.nodeTags[m.currentPath] = meta.Tags
	m.nodePinned[m.currentPath] = meta.Pinned
	if m.tagFilter != "" {
		m.rebuildFlat()
	}
	m.renderMarkdown()
	return true
}

func (m *Model) dirty() bool {
	return m.currentPath != "" && m.editor.Value() != m.original
}

func (m *Model) previewLineCount() int {
	if m.renderedContent == "" {
		return 0
	}
	return strings.Count(m.renderedContent, "\n") + 1
}

func (m *Model) toggleFocus() {
	m.focusing = !m.focusing
	if m.focusing {
		m.active = contentPane
		if m.mode == modeEdit {
			m.editor.Focus()
		}
		m.setStatus("Focus mode", false)
	} else {
		m.setStatus("Focus off", false)
	}
	m.resize(m.width, m.height)
}

func (m *Model) exitFocus() {
	m.focusing = false
	m.resize(m.width, m.height)
	m.setStatus("Focus off", false)
}

func (m Model) focusView() string {
	top := "≡"
	if m.currentPath != "" {
		top = "≡ " + truncate(m.currentPath, max(1, m.width-6))
	}
	topLine := lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render(" " + top)
	bottomLine := lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render("  Esc 退出专注")

	var body string
	if m.mode == modeEdit {
		body = m.editor.View()
	} else {
		body = m.preview.View()
	}
	body = lipgloss.Place(m.width, max(1, m.height-2), lipgloss.Center, lipgloss.Top, body)
	view := topLine + "\n" + body + "\n" + bottomLine
	return lipgloss.NewStyle().Background(bg).Width(m.width).Height(m.height).Render(view)
}

func (m Model) inNoteSearchFocusView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Search note") + "\n\n")
	b.WriteString(m.input.View() + "\n")
	if len(m.searchMatches) == 0 {
		b.WriteString("\n" + errorSty.Render("No matches"))
	} else {
		b.WriteString("\n" + statusSty.Render(fmt.Sprintf("%d / %d matches", m.searchIndex+1, len(m.searchMatches))))
	}
	b.WriteString("\n\n" + mutedSty.Render("Enter next · Shift+Enter prev · Esc close"))
	return m.bottomOverlay(b.String())
}

func (m *Model) startGlobalSearch() {
	m.beforeGlobalSearch = m.mode
	m.mode = modeSearchGlobal
	m.active = contentPane
	m.globalSearchQuery = ""
	m.globalSearchResults = nil
	m.globalSearchIndex = 0
	m.status = ""
	m.input.Prompt = "Search everywhere: "
	m.input.Placeholder = "type to search"
	m.input.SetValue("")
	m.input.Width = max(10, min(50, m.width-12))
	m.input.Focus()
}

func (m Model) updateGlobalSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitGlobalSearch()
		return m, nil
	case "enter":
		m.openGlobalSearchResult()
		return m, nil
	case "up", "shift+tab":
		m.moveGlobalSearch(-1)
		return m, nil
	case "down", "tab":
		m.moveGlobalSearch(1)
		return m, nil
	case "home":
		m.globalSearchIndex = 0
		return m, nil
	case "end":
		m.globalSearchIndex = max(0, len(m.globalSearchResults)-1)
		return m, nil
	case "pgup":
		m.moveGlobalSearch(-10)
		return m, nil
	case "pgdown":
		m.moveGlobalSearch(10)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	query := m.input.Value()
	if query == m.globalSearchQuery {
		return m, cmd
	}
	m.globalSearchQuery = query
	if strings.TrimSpace(query) == "" {
		m.globalSearchResults = nil
		m.globalSearchIndex = 0
		return m, nil
	}
	q := query
	return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return globalSearchMsg{query: q}
	})
}

func (m *Model) exitGlobalSearch() {
	mode := m.beforeGlobalSearch
	m.mode = mode
	m.input.Blur()
	m.input.Prompt = "› "
	m.globalSearchQuery = ""
	m.globalSearchResults = nil
	m.globalSearchIndex = 0
	m.renderMarkdown()
	m.adjustPreviewHeight()
	if mode == modeEdit && m.currentPath != "" {
		m.active = contentPane
		m.setEditorBackground(surface)
		m.editor.Focus()
	} else {
		m.active = contentPane
		m.setEditorBackground(bg)
	}
}

func (m *Model) runGlobalSearch(query string) {
	m.globalSearchQuery = query
	m.globalSearchResults = nil
	m.globalSearchIndex = 0
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return
	}
	var results []globalSearchResult
	var walk func([]*storage.Node)
	walk = func(nodes []*storage.Node) {
		for _, n := range nodes {
			if n.IsDir {
				walk(n.Children)
				continue
			}
			title := strings.TrimSuffix(n.Name, filepath.Ext(n.Name))
			if strings.Contains(strings.ToLower(title), q) {
				results = append(results, globalSearchResult{path: n.RelPath, title: title, snippet: title, lineNum: 1})
			}
			if len(results) >= 20 {
				break
			}
			content, err := m.store.Read(n.RelPath)
			if err != nil {
				continue
			}
			for li, line := range strings.Split(content, "\n") {
				if strings.Contains(strings.ToLower(line), q) {
					results = append(results, globalSearchResult{path: n.RelPath, title: title, snippet: searchSnippet(line, q, 56), lineNum: li + 1})
				}
				if len(results) >= 20 {
					break
				}
			}
		}
	}
	walk(m.tree)
	if len(results) > 20 {
		results = results[:20]
	}
	m.globalSearchResults = results
}

func (m *Model) moveGlobalSearch(delta int) {
	n := len(m.globalSearchResults)
	if n == 0 {
		return
	}
	m.globalSearchIndex = ((m.globalSearchIndex+delta)%n + n) % n
}

func (m *Model) openGlobalSearchResult() {
	if len(m.globalSearchResults) == 0 || m.globalSearchIndex < 0 || m.globalSearchIndex >= len(m.globalSearchResults) {
		return
	}
	r := m.globalSearchResults[m.globalSearchIndex]
	if !m.openPath(r.path) {
		return
	}
	m.pushHistory(r.path)
	mode := m.beforeGlobalSearch
	m.mode = mode
	m.active = contentPane
	m.input.Blur()
	m.input.Prompt = "› "
	m.globalSearchQuery = ""
	m.globalSearchResults = nil
	m.globalSearchIndex = 0
	if mode == modeEdit {
		m.setEditorBackground(surface)
		m.editor.Focus()
	} else {
		m.setEditorBackground(bg)
	}
	m.renderMarkdown()
	m.adjustPreviewHeight()
	if r.lineNum > 0 {
		m.performGotoLine(strconv.Itoa(r.lineNum))
	}
}

func (m *Model) startQuickOpen() {
	m.beforeQuickOpen = m.mode
	m.mode = modeQuickOpen
	m.active = contentPane
	m.quickOpenQuery = ""
	m.quickOpenResults = nil
	m.quickOpenIndex = 0
	m.status = ""
	m.input.Prompt = "Open: "
	m.input.Placeholder = "note name"
	m.input.SetValue("")
	m.input.Width = max(10, min(50, m.width-12))
	m.input.Focus()
	m.runQuickOpen("")
}

func (m Model) updateQuickOpen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitQuickOpen()
		return m, nil
	case "enter":
		m.openQuickOpenResult()
		return m, nil
	case "up", "shift+tab":
		m.moveQuickOpen(-1)
		return m, nil
	case "down", "tab":
		m.moveQuickOpen(1)
		return m, nil
	case "home":
		m.quickOpenIndex = 0
		return m, nil
	case "end":
		m.quickOpenIndex = max(0, len(m.quickOpenResults)-1)
		return m, nil
	case "pgup":
		m.moveQuickOpen(-10)
		return m, nil
	case "pgdown":
		m.moveQuickOpen(10)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	q := m.input.Value()
	if q != m.quickOpenQuery {
		m.quickOpenQuery = q
		m.runQuickOpen(q)
	}
	return m, cmd
}

func (m *Model) exitQuickOpen() {
	mode := m.beforeQuickOpen
	m.mode = mode
	m.active = contentPane
	m.input.Blur()
	m.input.Prompt = "› "
	m.quickOpenQuery = ""
	m.quickOpenResults = nil
	m.quickOpenIndex = 0
	m.renderMarkdown()
	m.adjustPreviewHeight()
	if mode == modeEdit && m.currentPath != "" {
		m.setEditorBackground(surface)
		m.editor.Focus()
	} else {
		m.setEditorBackground(bg)
	}
}

func (m *Model) runQuickOpen(query string) {
	m.quickOpenResults = nil
	m.quickOpenIndex = 0
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		m.quickOpenResults = m.recentQuickOpenResults()
		return
	}
	type candidate struct {
		path       string
		title      string
		dir        string
		titleMatch bool
	}
	var cands []candidate
	var walk func([]*storage.Node)
	walk = func(nodes []*storage.Node) {
		for _, n := range nodes {
			if n.IsDir {
				walk(n.Children)
				continue
			}
			title := strings.TrimSuffix(n.Name, filepath.Ext(n.Name))
			titleLower := strings.ToLower(title)
			if !strings.Contains(titleLower, q) && !strings.Contains(strings.ToLower(n.RelPath), q) {
				continue
			}
			cands = append(cands, candidate{
				path:       n.RelPath,
				title:      title,
				dir:        filepath.Dir(n.RelPath),
				titleMatch: strings.Contains(titleLower, q),
			})
			if len(cands) >= 50 {
				break
			}
		}
	}
	walk(m.tree)
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].titleMatch != cands[j].titleMatch {
			return cands[i].titleMatch
		}
		return strings.ToLower(cands[i].path) < strings.ToLower(cands[j].path)
	})
	if len(cands) > 20 {
		cands = cands[:20]
	}
	for _, c := range cands {
		m.quickOpenResults = append(m.quickOpenResults, quickOpenResult{path: c.path, title: c.title, dir: c.dir})
	}
}

// recentQuickOpenResults returns recently opened notes that still exist on
// disk, most recent first, for the empty Quick Open query.
func (m Model) recentQuickOpenResults() []quickOpenResult {
	exists := make(map[string]bool)
	var collect func([]*storage.Node)
	collect = func(nodes []*storage.Node) {
		for _, n := range nodes {
			if n.IsDir {
				collect(n.Children)
				continue
			}
			exists[n.RelPath] = true
		}
	}
	collect(m.tree)

	var out []quickOpenResult
	for _, path := range m.recent {
		if !exists[path] {
			continue
		}
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		out = append(out, quickOpenResult{path: path, title: title, dir: filepath.Dir(path)})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func (m *Model) moveQuickOpen(delta int) {
	n := len(m.quickOpenResults)
	if n == 0 {
		return
	}
	m.quickOpenIndex = ((m.quickOpenIndex+delta)%n + n) % n
}

func (m *Model) openQuickOpenResult() {
	if m.quickOpenIndex < 0 || m.quickOpenIndex >= len(m.quickOpenResults) {
		return
	}
	r := m.quickOpenResults[m.quickOpenIndex]
	if !m.openPath(r.path) {
		return
	}
	m.pushHistory(r.path)
	m.exitQuickOpen()
}

func (m Model) quickOpenView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Quick open") + "\n\n")
	b.WriteString(m.input.View() + "\n")
	if len(m.quickOpenResults) == 0 {
		if strings.TrimSpace(m.quickOpenQuery) == "" {
			b.WriteString("\n" + mutedSty.Render("Type to filter notes by name"))
		} else {
			b.WriteString("\n" + errorSty.Render("No matching notes"))
		}
	} else {
		if strings.TrimSpace(m.quickOpenQuery) == "" {
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Recent") + "\n")
		}
		for i, r := range m.quickOpenResults {
			b.WriteString("\n" + m.quickOpenResultRow(r, i == m.quickOpenIndex))
		}
	}
	b.WriteString("\n\n" + mutedSty.Render("Type to filter notes · ↑/↓ select · Enter open · Esc cancel"))
	return m.bottomOverlay(b.String())
}

func (m Model) quickOpenResultRow(r quickOpenResult, selected bool) string {
	var title string
	if r.dir != "." {
		title = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(r.dir+"/") + lipgloss.NewStyle().Foreground(text).Bold(true).Render(r.title)
	} else {
		title = lipgloss.NewStyle().Foreground(text).Bold(true).Render(r.title)
	}
	row := title
	if selected {
		row = lipgloss.NewStyle().Background(selection).Foreground(text).Render(row)
	}
	return row
}

func (m Model) globalSearchView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Global search") + "\n\n")
	b.WriteString(m.input.View() + "\n")
	if len(m.globalSearchResults) == 0 {
		if strings.TrimSpace(m.globalSearchQuery) == "" {
			b.WriteString("\n" + mutedSty.Render("Type to search all notes"))
		} else {
			b.WriteString("\n" + errorSty.Render("No matches"))
		}
	} else {
		for i, r := range m.globalSearchResults {
			if i >= 20 {
				break
			}
			b.WriteString("\n" + m.globalSearchResultRow(r, i == m.globalSearchIndex))
		}
	}
	b.WriteString("\n\n" + mutedSty.Render("Type to search · ↑/↓ select · Enter to go to line · Esc to close"))
	return m.bottomOverlay(b.String())
}

func (m Model) globalSearchResultRow(r globalSearchResult, selected bool) string {
	folder := filepath.Dir(r.path)
	var title string
	if folder != "." {
		title = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(folder+"/") + lipgloss.NewStyle().Foreground(text).Bold(true).Render(r.title)
	} else {
		title = lipgloss.NewStyle().Foreground(text).Bold(true).Render(r.title)
	}
	line := ""
	if r.lineNum > 0 {
		line = mutedSty.Render(fmt.Sprintf("line %d: ", r.lineNum))
	}
	snippet := highlightKeyword(r.snippet, m.globalSearchQuery)
	row := title + "   " + line + snippet
	if selected {
		row = lipgloss.NewStyle().Background(selection).Foreground(text).Render(row)
	}
	return row
}

func searchSnippet(line, query string, max int) string {
	line = strings.TrimSpace(line)
	q := strings.ToLower(query)
	if q == "" {
		return truncate(line, max)
	}
	idx := strings.Index(strings.ToLower(line), q)
	if idx < 0 {
		return truncate(line, max)
	}
	start := idx - max/3
	if start < 0 {
		start = 0
	}
	end := idx + len(q) + max*2/3
	if end > len(line) {
		end = len(line)
	}
	snippet := strings.TrimSpace(line[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(line) {
		snippet = snippet + "…"
	}
	return snippet
}

func (m Model) bottomOverlay(content string) string {
	panel := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(82, max(44, m.width-6))).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Bottom, panel, lipgloss.WithWhitespaceBackground(bg))
}

func highlightKeyword(s, query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return s
	}
	lower := strings.ToLower(s)
	if !strings.Contains(lower, q) {
		return s
	}
	var b strings.Builder
	rest := s
	lo := lower
	for {
		idx := strings.Index(lo, q)
		if idx < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:idx])
		b.WriteString(keywordSty.Render(rest[idx : idx+len(q)]))
		rest = rest[idx+len(q):]
		lo = lo[idx+len(q):]
	}
	return b.String()
}

func (m *Model) startSearch() {
	m.mode = modeSearch
	m.active = contentPane
	m.searchQuery = ""
	m.searchMatches = nil
	m.searchIndex = 0
	m.status = ""
	m.input.Prompt = "Search: "
	m.input.Placeholder = "type to search"
	m.input.SetValue("")
	m.input.Width = max(10, min(50, m.width-12))
	m.input.Focus()
	m.preview.SetContent(m.renderedContent)
	m.adjustPreviewHeight()
}

func (m *Model) exitSearch() {
	m.mode = modeNormal
	m.active = contentPane
	m.searchQuery = ""
	m.searchMatches = nil
	m.searchIndex = 0
	m.input.Blur()
	m.input.Prompt = "› "
	m.preview.SetContent(m.renderedContent)
	m.adjustPreviewHeight()
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	focused := m.input.Focused()
	switch msg.String() {
	case "esc":
		m.exitSearch()
		return m, nil
	case "enter":
		m.nextSearchMatch(1)
		m.blurSearchInput()
		return m, nil
	case "shift+enter":
		m.nextSearchMatch(-1)
		m.blurSearchInput()
		return m, nil
	case "n":
		if !focused {
			m.nextSearchMatch(1)
			return m, nil
		}
	case "N":
		if !focused {
			m.nextSearchMatch(-1)
			return m, nil
		}
	}
	if !focused {
		m.input.Focus()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	query := m.input.Value()
	if query != m.searchQuery {
		m.searchQuery = query
		m.searchMatches = findSearchMatches(m.renderedPlain, query)
		m.searchIndex = 0
		m.applySearchHighlight()
		m.scrollToCurrentMatch()
	}
	return m, cmd
}

func (m *Model) blurSearchInput() {
	if m.input.Focused() {
		m.input.Blur()
	}
}

func (m *Model) applySearchHighlight() {
	if m.searchQuery == "" {
		m.preview.SetContent(m.renderedContent)
		return
	}
	m.preview.SetContent(highlightSearchContent(m.renderedContent, m.searchQuery))
}

func (m *Model) nextSearchMatch(delta int) {
	n := len(m.searchMatches)
	if n == 0 {
		return
	}
	m.searchIndex = ((m.searchIndex+delta)%n + n) % n
	m.scrollToCurrentMatch()
}

func (m *Model) scrollToCurrentMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	idx := m.searchIndex
	if idx < 0 || idx >= len(m.searchMatches) {
		return
	}
	m.scrollPreviewToLine(m.searchMatches[idx].line)
}

func (m *Model) scrollPreviewToLine(line0 int) {
	total := m.previewLineCount()
	visible := max(1, m.preview.Height)
	if total <= visible {
		m.preview.SetYOffset(0)
		return
	}
	target := line0 - visible/2
	target = max(0, min(total-visible, target))
	m.preview.SetYOffset(target)
}

func (m *Model) adjustPreviewHeight() {
	base := max(1, m.contentHeight()-2)
	if m.mode == modeSearch {
		base = max(1, base-1)
	}
	m.preview.Height = base
}

func (m *Model) startGotoLine() {
	if m.currentPath == "" {
		m.flashStatus("Open a note first", true, 2*time.Second)
		return
	}
	m.beforePrompt = m.mode
	m.promptKind = promptGotoLine
	m.mode = modePrompt
	m.statusErr = false
	m.input.SetValue("")
	m.input.Placeholder = "Line number:"
	m.input.Prompt = "Go to line: "
	m.input.Width = max(8, min(20, m.width-14))
	m.input.Focus()
}

func (m Model) updateGotoLinePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = m.beforePrompt
		m.input.Blur()
		m.input.Prompt = "› "
		m.restoreEditFocus()
		return m, nil
	case "enter":
		value := m.input.Value()
		m.mode = m.beforePrompt
		m.input.Blur()
		m.input.Prompt = "› "
		m.restoreEditFocus()
		m.performGotoLine(value)
		return m, nil
	}
	if !isGotoLineKey(msg) {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func isGotoLineKey(msg tea.KeyMsg) bool {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		r := msg.Runes[0]
		return r >= '0' && r <= '9'
	}
	switch msg.Type {
	case tea.KeyBackspace, tea.KeyDelete, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd, tea.KeyCtrlA, tea.KeyCtrlE, tea.KeyCtrlK, tea.KeyCtrlU, tea.KeyCtrlW:
		return true
	}
	return false
}

func (m *Model) performGotoLine(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	line, err := strconv.Atoi(value)
	if err != nil || line < 1 {
		m.flashStatus("Invalid line number", true, 2*time.Second)
		return
	}
	if m.mode == modeEdit {
		total := m.editor.LineCount()
		if line > total {
			line = total
			m.flashStatus("Line out of range · jumped to last line", true, 2*time.Second)
		} else {
			m.setStatus(fmt.Sprintf("Jumped to line %d", line), false)
		}
		m.gotoLineEdit(line - 1)
		return
	}
	total := m.previewLineCount()
	if line > total {
		line = total
		m.flashStatus("Line out of range · jumped to last line", true, 2*time.Second)
	}
	m.scrollPreviewToLine(line - 1)
}

func (m *Model) gotoLineEdit(line int) {
	if line < 0 {
		line = 0
	}
	if line > m.editor.LineCount()-1 {
		line = m.editor.LineCount() - 1
	}
	current := m.editor.Line()
	for current > line {
		m.editor.CursorUp()
		current--
	}
	for current < line {
		m.editor.CursorDown()
		current++
	}
	m.editor.SetCursor(0)
}

func (m *Model) restoreEditFocus() {
	if m.mode == modeEdit {
		m.editor.Focus()
	}
}

// tryPasteImage checks the clipboard for an image and saves it if found.
// Returns the relative path reference for markdown insertion.
func (m *Model) tryPasteImage() (string, bool) {
	data, _, err := readImageFromClipboard()
	if err != nil || len(data) == 0 {
		return "", false
	}
	ref, err := m.images.saveImage(data)
	if err != nil {
		m.flashStatus("Image paste failed: "+err.Error(), true, 3*time.Second)
		return "", false
	}
	return ref, true
}

// insertImageRef inserts a markdown image reference at the cursor position.
func (m *Model) insertImageRef(ref string) {
	current := m.editor.Value()
	lines := strings.Split(current, "\n")
	row := m.editor.Line()
	if row < len(lines) {
		lines[row] = lines[row] + "\n\n![image](" + ref + ")\n"
	} else {
		lines = append(lines, "![image](" + ref + ")")
	}
	m.editor.SetValue(strings.Join(lines, "\n"))
	m.gotoLineEdit(row + 2)
	m.editor.SetCursor(0)
	m.flashStatus("✓ Pasted image: "+filepath.Base(ref), false, 2*time.Second)
}

func (m *Model) handleEditEnter(before string, beforePos cursorPos) bool {
	pos := m.cursorPos()
	line := m.currentLineText()
	if pos.col < len([]rune(line)) {
		return false
	}
	if m.handleListEnter() {
		m.recordEdit(before, m.editor.Value(), beforePos, m.cursorPos())
		return true
	}
	if indent := leadingIndent(line); indent != "" {
		m.editor.InsertString("\n" + indent)
		m.recordEdit(before, m.editor.Value(), beforePos, m.cursorPos())
		return true
	}
	return false
}

func (m *Model) handleListEnter() bool {
	line := m.currentLineText()
	if isEmptyListLine(line) {
		m.clearEmptyListLine()
		return true
	}
	if cont := listContinuation(line); cont != "" {
		m.editor.InsertString("\n" + cont)
		return true
	}
	return false
}

func (m *Model) clearEmptyListLine() {
	lines := strings.Split(m.editor.Value(), "\n")
	row := m.editor.Line()
	if row < 0 || row >= len(lines) {
		return
	}
	lines[row] = ""
	m.editor.SetValue(strings.Join(lines, "\n"))
	m.gotoLineEdit(row)
	m.editor.SetCursor(0)
}

func (m *Model) refresh(preferred string) {
	tree, err := m.store.Tree()
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.tree = tree
	if len(m.expanded) == 0 {
		for _, n := range tree {
			if n.IsDir {
				m.expanded[n.RelPath] = true
			}
		}
	}
	m.rebuildFlat()
	if preferred != "" {
		m.selectPath(preferred)
	} else if m.selected >= len(m.flat) {
		m.selected = max(0, len(m.flat)-1)
	}
	m.ensureSelectionVisible()
}

func (m *Model) rebuildFlat() {
	m.flat = m.flat[:0]
	m.nodeTags = make(map[string][]string)
	filter := strings.ToLower(strings.TrimSpace(m.tagFilter))
	var walk func([]*storage.Node, int)
	walk = func(nodes []*storage.Node, depth int) {
		nodes = m.sortedNodes(nodes)
		for _, n := range nodes {
			if n.IsDir {
				m.flat = append(m.flat, flatNode{node: n, depth: depth})
				if m.expanded[n.RelPath] {
					walk(n.Children, depth+1)
				}
				continue
			}
			meta := m.nodeMeta(n)
			tags := meta.Tags
			m.nodeTags[n.RelPath] = tags
			m.nodePinned[n.RelPath] = meta.Pinned
			if filter != "" && !containsTag(tags, filter) {
				continue
			}
			m.flat = append(m.flat, flatNode{node: n, depth: depth})
		}
	}
	walk(m.tree, 0)
}

func (m *Model) sortedNodes(nodes []*storage.Node) []*storage.Node {
	sorted := make([]*storage.Node, len(nodes))
	copy(sorted, nodes)
	switch m.sortMode {
	case sortByModified:
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].IsDir != sorted[j].IsDir {
				return sorted[i].IsDir
			}
			if m.nodePinned[sorted[i].RelPath] != m.nodePinned[sorted[j].RelPath] {
				return m.nodePinned[sorted[i].RelPath]
			}
			return m.nodeModTime(sorted[i]).After(m.nodeModTime(sorted[j]))
		})
	case sortByCreated:
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].IsDir != sorted[j].IsDir {
				return sorted[i].IsDir
			}
			if m.nodePinned[sorted[i].RelPath] != m.nodePinned[sorted[j].RelPath] {
				return m.nodePinned[sorted[i].RelPath]
			}
			return m.nodeCreatedTime(sorted[i]).After(m.nodeCreatedTime(sorted[j]))
		})
	default:
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].IsDir != sorted[j].IsDir {
				return sorted[i].IsDir
			}
			if m.nodePinned[sorted[i].RelPath] != m.nodePinned[sorted[j].RelPath] {
				return m.nodePinned[sorted[i].RelPath]
			}
			return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
		})
	}
	return sorted
}

func (m *Model) nodeModTime(n *storage.Node) time.Time {
	info, err := os.Stat(filepath.Join(m.store.Root, n.RelPath))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (m *Model) nodeCreatedTime(n *storage.Node) time.Time {
	if !n.IsDir {
		if content, err := m.store.Read(n.RelPath); err == nil {
			if meta, _ := parseFrontMatter(content); meta.Created != "" {
				if t, err := time.Parse("2006-01-02", meta.Created); err == nil {
					return t
				}
			}
		}
	}
	return m.nodeModTime(n)
}

func (m *Model) nodeMeta(n *storage.Node) FrontMatter {
	content, err := m.store.Read(n.RelPath)
	if err != nil {
		return FrontMatter{}
	}
	meta, _ := parseFrontMatter(content)
	return meta
}

func (m Model) tagsRow(tags []string, width int) string {
	if len(tags) == 0 {
		return ""
	}
	width = max(4, width)
	var lines []string
	var b strings.Builder
	for i, tag := range tags {
		chip := lipgloss.NewStyle().Foreground(accent).Render("▍ " + tag)
		sep := 0
		if i > 0 {
			sep = 1
		}
		if i > 0 && lipgloss.Width(b.String())+sep+lipgloss.Width(chip) > width {
			lines = append(lines, b.String())
			b.Reset()
			b.WriteString(" ")
			sep = 0
		}
		if sep > 0 {
			b.WriteString(" ")
		}
		b.WriteString(chip)
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func (m *Model) expandParents(path string) {
	parent := filepath.Dir(path)
	for parent != "." && parent != "" {
		m.expanded[parent] = true
		parent = filepath.Dir(parent)
	}
}

func (m *Model) selectPath(path string) {
	for i, item := range m.flat {
		if item.node.RelPath == path {
			m.selected = i
			m.ensureSelectionVisible()
			return
		}
	}
}

func (m *Model) selectedPath() string {
	if len(m.flat) == 0 || m.selected < 0 || m.selected >= len(m.flat) {
		return ""
	}
	return m.flat[m.selected].node.RelPath
}

func (m *Model) selectedParent() string {
	if len(m.flat) == 0 {
		return ""
	}
	n := m.flat[m.selected].node
	if n.IsDir {
		return n.RelPath
	}
	parent := filepath.Dir(n.RelPath)
	if parent == "." {
		return ""
	}
	return parent
}

func (m *Model) ensureSelectionVisible() {
	rows := m.treeRows()
	if m.selected < m.treeOffset {
		m.treeOffset = m.selected
	}
	if m.selected >= m.treeOffset+rows {
		m.treeOffset = m.selected - rows + 1
	}
	m.treeOffset = max(0, m.treeOffset)
}

func (m *Model) treeRows() int { return max(1, m.bodyHeight-2) }

func (m *Model) resize(width, height int) {
	m.width, m.height = max(1, width), max(1, height)
	m.bodyHeight = max(3, m.height-2)

	if m.focusing {
		ew := max(10, m.width-4)
		eh := max(1, m.height-2)
		m.editor.SetWidth(ew)
		m.editor.SetHeight(eh)
		m.preview.Width = max(10, ew)
		m.preview.Height = max(1, eh)
		m.renderMarkdown()
		return
	}

	// Three-tier width response:
	//   narrow (<=60)  → single panel, switch via Tab
	//   medium (61-99) → narrow tree (~20-25)
	//   wide (≥100)    → comfortable tree (~30-35)
	if m.width <= 60 {
		m.compact = true
		m.treeWidth = m.width
	} else if m.width < 100 {
		m.compact = false
		m.treeWidth = max(20, min(25, m.width/4))
	} else {
		m.compact = false
		m.treeWidth = max(28, min(36, m.width/3))
	}

	contentWidth := m.width
	if !m.compact && m.treeVisible {
		contentWidth = max(1, m.width-m.treeWidth-1)
	}
	m.editor.SetWidth(max(10, contentWidth-4))
	m.editor.SetHeight(max(1, m.contentHeight()-2))
	m.preview.Width = max(10, contentWidth-4)
	m.adjustPreviewHeight()
	m.ensureSelectionVisible()
	m.renderMarkdown()
	if m.mode == modeHelp {
		m.renderHelpContent()
	}
}

func (m *Model) renderMarkdown() {
	if m.currentPath == "" {
		m.preview.SetContent("")
		m.renderedContent = ""
		return
	}
	width := max(20, m.preview.Width-1)
	// Glamour renderers are expensive to construct; reuse one until the wrap
	// width (the only dynamic rendering input) changes.
	if m.renderer == nil || m.rendererWidth != width {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(markdownStyle()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			m.setStatus("Markdown renderer: "+err.Error(), true)
			m.preview.SetContent(m.editor.Value())
			m.renderedContent = m.editor.Value()
			m.renderedPlain = m.editor.Value()
			return
		}
		m.renderer = renderer
		m.rendererWidth = width
	}
	// Pre-process: replace image syntax with terminal-safe placeholders before
	// glamour renders it (glamour would otherwise emit unhelpful alt text).
	preprocessed := renderImagesInMarkdown(m.editor.Value())
	rendered, err := m.renderer.Render(preprocessed)
	if err != nil {
		m.setStatus("Markdown preview: "+err.Error(), true)
		m.preview.SetContent(m.editor.Value())
		m.renderedContent = m.editor.Value()
		m.renderedPlain = m.editor.Value()
		return
	}
	content := strings.Trim(decorateCodeBlocks(rendered, width), "\n")
	m.preview.SetContent(content)
	m.renderedContent = content
	m.renderedPlain = extractPlainText(content)
}

func markdownStyle() glamansi.StyleConfig {
	return glamansi.StyleConfig{
		Document: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{Color: stringPtr(textColor)},
			Margin:         uintPtr(1),
		},
		BlockQuote: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color:  stringPtr(mutedColor),
				Italic: boolPtr(true),
			},
			Indent:      uintPtr(1),
			IndentToken: stringPtr("│ "),
		},
		List: glamansi.StyleList{LevelIndent: 2},
		Heading: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(accentColor), Bold: boolPtr(true), BlockSuffix: "\n",
			},
		},
		H1: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(accentColor), Bold: boolPtr(true), Upper: boolPtr(true),
				BlockSuffix: "\n────────",
			},
		},
		H2: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(accentColor), Bold: boolPtr(true), Prefix: "## ",
			},
		},
		H3: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(textColor), Bold: boolPtr(true), Prefix: "### ",
			},
		},
		H4: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(mutedColor), Bold: boolPtr(true), Prefix: "#### ",
			},
		},
		H5: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(mutedColor), Bold: boolPtr(true), Prefix: "##### ",
			},
		},
		H6: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(mutedColor), Bold: boolPtr(true), Prefix: "###### ",
			},
		},
		Strong: glamansi.StylePrimitive{Bold: boolPtr(true)},
		Emph:   glamansi.StylePrimitive{Italic: boolPtr(true)},
		HorizontalRule: glamansi.StylePrimitive{
			Color: stringPtr(mutedColor), Format: "\n────────\n",
		},
		Item:        glamansi.StylePrimitive{BlockPrefix: "• "},
		Enumeration: glamansi.StylePrimitive{BlockPrefix: ". "},
		Task:        glamansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:        glamansi.StylePrimitive{Color: stringPtr(accentColor), Underline: boolPtr(true)},
		LinkText:    glamansi.StylePrimitive{Color: stringPtr(accentColor)},
		Code: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Prefix: " ", Suffix: " ", Color: stringPtr(textColor), BackgroundColor: stringPtr(surfaceColor),
		}},
		CodeBlock: glamansi.StyleCodeBlock{StyleBlock: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(textColor), BackgroundColor: stringPtr(surfaceColor),
				BlockPrefix: codeBlockStart + "\n", BlockSuffix: codeBlockEnd,
			},
			Margin: uintPtr(1),
		}, Chroma: &glamansi.Chroma{
			Text:                glamansi.StylePrimitive{Color: stringPtr(textColor)},
			Background:          glamansi.StylePrimitive{BackgroundColor: stringPtr(surfaceColor)},
			Comment:             glamansi.StylePrimitive{Color: stringPtr(mutedColor), Italic: boolPtr(true)},
			CommentPreproc:      glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Keyword:             glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			KeywordType:         glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			Name:                glamansi.StylePrimitive{Color: stringPtr(textColor)},
			NameBuiltin:         glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			NameTag:             glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			NameAttribute:       glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			NameClass:           glamansi.StylePrimitive{Color: stringPtr(textColor), Bold: boolPtr(true)},
			NameConstant:        glamansi.StylePrimitive{Color: stringPtr(textColor)},
			NameFunction:        glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			Literal:             glamansi.StylePrimitive{Color: stringPtr(textColor)},
			LiteralNumber:       glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			LiteralString:       glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			LiteralStringEscape: glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Operator:            glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Punctuation:         glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
			GenericDeleted:      glamansi.StylePrimitive{Color: stringPtr(dangerColor)},
			GenericInserted:     glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			GenericEmph:         glamansi.StylePrimitive{Italic: boolPtr(true)},
			GenericStrong:       glamansi.StylePrimitive{Bold: boolPtr(true)},
			GenericSubheading:   glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
		}},
		Table: glamansi.StyleTable{
			StyleBlock: glamansi.StyleBlock{
				StylePrimitive: glamansi.StylePrimitive{
					Color: stringPtr(accentColor),
				},
				Margin: uintPtr(1),
			},
			CenterSeparator: stringPtr("│"),
			ColumnSeparator: stringPtr("│"),
			RowSeparator:    stringPtr("─"),
		},
	}
}

const (
	codeBlockStart = "\x01CKSTART\x01"
	codeBlockEnd   = "\x01CKEND\x01"
)

var ansiRegexp = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRegexp.ReplaceAllString(s, "") }

// extractPlainText converts a rendered ANSI string into clean copyable text:
// ANSI escapes are removed along with code-block markers and other control
// characters, and trailing padding added to code lines is trimmed.
// Whitespace control characters (\n, 	, \r) are preserved so multi-line text
// stays readable when pasted.
func extractPlainText(rendered string) string {
	s := stripANSI(rendered)
	s = strings.ReplaceAll(s, codeBlockStart, "")
	s = strings.ReplaceAll(s, codeBlockEnd, "")
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '	' && r != '\r') || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	lines := strings.Split(b.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " 	")
	}
	return strings.Join(lines, "\n")
}

func plainTextLine(s string) string {
	s = stripANSI(s)
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t' && r != '\r') || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func findSearchMatches(plain, query string) []matchPos {
	var matches []matchPos
	if query == "" {
		return matches
	}
	qLower := []rune(strings.ToLower(query))
	if len(qLower) == 0 {
		return matches
	}
	for li, line := range strings.Split(plain, "\n") {
		lower := []rune(strings.ToLower(line))
		if len(qLower) > len(lower) {
			continue
		}
		for i := 0; i+len(qLower) <= len(lower); {
			if string(lower[i:i+len(qLower)]) == string(qLower) {
				matches = append(matches, matchPos{line: li, start: i, end: i + len(qLower)})
				i += len(qLower)
			} else {
				i++
			}
		}
	}
	return matches
}

const maxSearchHighlight = 200

func highlightSearchContent(content, query string) string {
	if query == "" {
		return content
	}
	hi := "\x1b[7m"
	hiEnd := "\x1b[27m"
	lines := strings.Split(content, "\n")
	changed := false
	remaining := maxSearchHighlight
	for i, line := range lines {
		if remaining <= 0 {
			break
		}
		ranges := findSearchMatches(plainTextLine(line), query)
		if len(ranges) == 0 {
			continue
		}
		if len(ranges) > remaining {
			ranges = ranges[:remaining]
		}
		remaining -= len(ranges)
		lines[i] = applyHighlightRanges(line, ranges, hi, hiEnd)
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
}

// highlightSelection highlights the span of text between start and end (byte
// offsets into content) using reverse video, leaving all other text untouched.
// Out-of-range or inverted offsets are clamped; a zero-length span returns the
// content unchanged.
func highlightSelection(content string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	if end <= start {
		return content
	}
	return content[:start] + "\x1b[7m" + content[start:end] + "\x1b[27m" + content[end:]
}

func applyHighlightRanges(line string, ranges []matchPos, hi, hiEnd string) string {
	if len(ranges) == 0 {
		return line
	}
	startAt := make(map[int]bool, len(ranges))
	endAt := make(map[int]bool, len(ranges))
	for _, r := range ranges {
		startAt[r.start] = true
		endAt[r.end] = true
	}
	var b strings.Builder
	vis := 0
	i := 0
	highlightOpen := false
	for i < len(line) {
		if line[i] == 0x1b {
			j := i + 1
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				j++
			}
			b.WriteString(line[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if (r < 0x20 && r != '	') || r == 0x7f {
			b.WriteRune(r)
			i += size
			continue
		}
		if startAt[vis] {
			b.WriteString(hi)
			highlightOpen = true
		}
		b.WriteRune(r)
		vis++
		if endAt[vis] {
			b.WriteString(hiEnd)
			highlightOpen = false
		}
		i += size
	}
	// Close any highlight left open at end of line to prevent visual bleeding
	// into adjacent panels (tree/separator) via padding or wrapping.
	if highlightOpen {
		b.WriteString(hiEnd)
	}
	return b.String()
}

// selectionHighlightCodes returns the ANSI sequences that apply and release
// the drag-selection highlight. The highlight is built from a lipgloss style
// using the palette's selection background and text foreground; rendering an
// empty string yields the SGR sequence followed by a full reset, so we split
// on that trailing reset. In plain (no-color) profiles the rendered sequence
// is empty and the highlight degrades to a no-op.
func selectionHighlightCodes() (hi, hiEnd string) {
	hiEnd = "\x1b[0m"
	hi = strings.TrimSuffix(
		lipgloss.NewStyle().Background(selection).Foreground(text).Render(""),
		hiEnd,
	)
	return hi, hiEnd
}

// renderSelectionContent returns the rendered content with the rune range
// [start, end) — expressed in plain-text coordinates (m.renderedPlain) —
// wrapped in the selection highlight. The rendered and plain contents share a
// 1:1 line structure (see contentOffsetAt), so each line's visible rune span
// is mapped onto the rendered line using applyHighlightRanges.
func renderSelectionContent(content, plain string, start, end int) string {
	if content == "" || plain == "" || end <= start {
		return content
	}
	total := utf8.RuneCountInString(plain)
	start = max(0, min(start, total))
	end = min(end, total)
	if end <= start {
		return content
	}
	hi, hiEnd := selectionHighlightCodes()
	if hi == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	plainLines := strings.Split(plain, "\n")
	if len(lines) != len(plainLines) {
		return content
	}
	offset := 0
	changed := false
	for i, line := range plainLines {
		lineLen := utf8.RuneCountInString(line)
		lineStart := offset
		lineEnd := lineStart + lineLen
		offset = lineEnd + 1
		if end <= lineStart || start >= lineEnd {
			continue
		}
		s, e := max(0, start-lineStart), min(lineLen, end-lineStart)
		if s >= e {
			continue
		}
		lines[i] = applyHighlightRanges(lines[i], []matchPos{{start: s, end: e}}, hi, hiEnd)
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
}

var orderedListRe = regexp.MustCompile(`^([0-9]+)([.)]) `)

func leadingIndent(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

func listMarker(trimmed string) (string, bool) {
	switch {
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "+ "):
		return trimmed[:2], true
	case strings.HasPrefix(trimmed, "[ ] "), strings.HasPrefix(trimmed, "[x] "):
		return "[ ] ", true
	}
	if m := orderedListRe.FindString(trimmed); m != "" {
		return m, true
	}
	return "", false
}

func parseOrderedMarker(marker string) (int, string, bool) {
	m := orderedListRe.FindStringSubmatch(marker)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	return n, m[2], true
}

func isEmptyListLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	marker, ok := listMarker(trimmed)
	if !ok {
		return false
	}
	return strings.TrimSpace(trimmed[len(marker):]) == ""
}

func listContinuation(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]
	marker, ok := listMarker(trimmed)
	if !ok {
		return ""
	}
	if n, delim, ok := parseOrderedMarker(marker); ok {
		return fmt.Sprintf("%s%d%s ", indent, n+1, delim)
	}
	return indent + marker
}

// decorateCodeBlocks restores a solid surface background behind code blocks.
// Glamour's chroma formatter intentionally strips the block background, so we
// mark code block boundaries, then re-apply the background after every ANSI
// reset inside each code line.
func decorateCodeBlocks(rendered string, width int) string {
	lines := strings.Split(rendered, "\n")
	var out []string
	inCode := false
	codeBg := termansi.Style{}.BackgroundColor(termansi.TrueColor(surfaceRGB)).String()
	border := mutedSty.Render(strings.Repeat("─", width))
	for _, line := range lines {
		plain := stripANSI(line)
		if !inCode {
			if strings.Contains(plain, codeBlockStart) {
				inCode = true
				out = append(out, border)
				continue
			}
			out = append(out, line)
			continue
		}
		if strings.Contains(plain, codeBlockEnd) {
			if i := strings.Index(line, codeBlockEnd); i >= 0 {
				code := line[:i]
				if strings.TrimSpace(stripANSI(code)) != "" {
					out = append(out, styleCodeLine(code, codeBg, width))
				}
			}
			inCode = false
			continue
		}
		out = append(out, styleCodeLine(line, codeBg, width))
	}
	return strings.Join(out, "\n")
}

func styleCodeLine(line, bg string, width int) string {
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg)
	line = strings.ReplaceAll(line, termansi.ResetStyle, termansi.ResetStyle+bg)
	fill := width - lipgloss.Width(line)
	if fill < 0 {
		fill = 0
	}
	return bg + line + strings.Repeat(" ", fill) + termansi.ResetStyle
}

func stringPtr(value string) *string { return &value }
func boolPtr(value bool) *bool       { return &value }
func uintPtr(value uint) *uint       { return &value }

func (m *Model) setStatus(message string, isError bool) {
	m.status, m.statusErr, m.statusOK = message, isError, false
	m.statusID++
	m.statusCmd = nil
}

func (m *Model) flashStatus(message string, isError bool, d time.Duration) tea.Cmd {
	m.setStatus(message, isError)
	m.statusOK = !isError
	id := m.statusID
	m.statusCmd = tea.Tick(d, func(time.Time) tea.Msg { return statusClearMsg{id: id} })
	return m.statusCmd
}

func (m Model) View() string {
	if m.width <= 1 || m.height <= 1 {
		return "TN"
	}
	if m.mode == modeHelp {
		return m.helpView()
	}
	if m.mode == modeCommand {
		return m.commandView()
	}
	if m.mode == modeWebdavConfig {
		return m.webdavConfigView()
	}
	if m.mode == modeExport {
		return m.exportDialogView()
	}
	if m.mode == modeSearchGlobal {
		return m.globalSearchView()
	}
	if m.mode == modeQuickOpen {
		return m.quickOpenView()
	}
	if m.mode == modeTag {
		return m.tagEditView()
	}
	if (m.mode == modePrompt && m.promptKind != promptGotoLine) || m.mode == modeConfirm {
		return m.dialogView()
	}
	if m.focusing {
		if m.mode == modeSearch {
			return m.inNoteSearchFocusView()
		}
		return m.focusView()
	}

	header := m.headerView()
	var body string
	if m.compact {
		if m.treeVisible && m.active == treePane {
			body = m.treeView(m.width)
		} else {
			body = m.contentView(m.width)
		}
	} else if !m.treeVisible {
		body = m.contentView(m.width)
	} else {
		contentW := max(1, m.width-m.treeWidth-1)
		separator := strings.Repeat("│\n", m.bodyRenderHeight()-1) + "│"
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.treeViewSides(m.treeWidth, true, false),
			mutedSty.Render(separator),
			m.contentViewSides(contentW, false, true),
		)
	}
	var bottom string
	switch {
	case m.mode == modeSearch:
		bottom = m.searchBarView() + "\n" + m.searchStatusView()
	case m.mode == modePrompt && m.promptKind == promptGotoLine:
		bottom = m.gotoBarView()
	case m.mode == modeTagFilter:
		bottom = m.tagFilterBarView()
	default:
		bottom = m.statusView()
	}
	view := header + "\n" + body + "\n" + bottom
	return lipgloss.NewStyle().Background(bg).Width(m.width).Height(m.height).Render(view)
}

func (m Model) bodyRenderHeight() int {
	if m.mode == modeSearch {
		return max(1, m.bodyHeight-1)
	}
	return m.bodyHeight
}

func (m Model) headerView() string {
	brand := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("◆ tn")
	tabs := m.headerTabs()
	left := brand + mutedSty.Render("  │  ") + tabs

	name := "no note"
	if m.currentPath != "" {
		name = m.currentPath
		if m.dirty() {
			name += "  " + dangerStyle("● unsaved")
		}
	}
	var badge string
	if m.mode == modeEdit {
		badge = editBadge.Render(" EDIT ") + " "
	}
	right := badge + truncateANSI(name, max(1, m.width-lipgloss.Width(left)-lipgloss.Width(badge)-12))
	space := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	line := " " + left + strings.Repeat(" ", space) + right
	return headerSty.Width(m.width).MaxHeight(1).Render(line)
}

// headerTabAt maps a header-row x-coordinate to the pane tab it hits. It
// mirrors headerView's layout so clicks stay aligned when labels change.
func (m Model) headerTabAt(x int) (pane, bool) {
	notesStart := 1 + lipgloss.Width("◆ tn") + lipgloss.Width("  │  ")
	if !m.treeVisible {
		contentW := lipgloss.Width(m.contentTabLabel())
		if x >= notesStart && x < notesStart+contentW {
			return contentPane, true
		}
		return contentPane, false
	}
	notesW := lipgloss.Width(" 1 Notes ")
	contentStart := notesStart + notesW + 1
	contentW := lipgloss.Width(" 2 Preview ")
	if m.mode == modeEdit {
		contentW = lipgloss.Width(" 2 Edit ")
	}
	if x >= notesStart && x < notesStart+notesW {
		return treePane, true
	}
	if x >= contentStart && x < contentStart+contentW {
		return contentPane, true
	}
	return treePane, false
}

func (m Model) headerTabs() string {
	if !m.treeVisible {
		return m.contentTabLabel()
	}
	notesStyle := lipgloss.NewStyle().Foreground(muted)
	contentStyle := lipgloss.NewStyle().Foreground(muted)
	if m.active == treePane {
		notesStyle = notesStyle.Background(accent).Foreground(bg).Bold(true)
	} else {
		contentStyle = contentStyle.Background(accent).Foreground(bg).Bold(true)
	}
	contentLabel := "2 Preview"
	if m.mode == modeEdit {
		contentLabel = "2 Edit"
	}
	return notesStyle.Render(" 1 Notes ") + " " + contentStyle.Render(" "+contentLabel+" ")
}

func (m Model) contentTabLabel() string {
	contentStyle := lipgloss.NewStyle().Foreground(muted)
	if !m.treeVisible || m.active != treePane {
		contentStyle = contentStyle.Background(accent).Foreground(bg).Bold(true)
	}
	contentLabel := "2 Preview"
	if m.mode == modeEdit {
		contentLabel = "2 Edit"
	}
	return contentStyle.Render(" " + contentLabel + " ")
}

func (m Model) toolbarItems() []toolbarItem {
	items := []toolbarItem{
		{"? help", "help"}, {"# tag", "tagfilter"}, {"n note", "note"}, {"N folder", "folder"}, {"e edit/save", "edit"}, {"s save", "save"}, {"y copy", "copy"}, {"f find", "find"}, {"^G select", "select"}, {"r rename", "rename"}, {"x delete", "delete"}, {"q quit", "quit"},
	}
	if m.compact {
		label := "→ note"
		if m.active == contentPane {
			label = "← lists"
		}
		items = append(items, toolbarItem{label, "pane"})
	} else if m.treeVisible {
		items = append(items, toolbarItem{"t tree", "tree"})
	}
	return items
}

func (m Model) footerActionAt(x int) string {
	if m.mode == modeEdit {
		return ""
	}
	budget := m.toolbarBudget()
	position := 1
	for _, item := range m.toolbarItems() {
		key, label, _ := strings.Cut(item.label, " ")
		width := lipgloss.Width("[" + key + "] " + label + "  ")
		if position+width > budget+1 {
			break
		}
		if x >= position && x < position+width {
			return item.action
		}
		position += width
	}
	return ""
}

func (m Model) toolbarActionAt(x int) string {
	return m.footerActionAt(x)
}

func (m Model) toolbarView() string {
	return m.shortcutBar()
}

func (m Model) treeView(width int) string {
	return m.treeViewSides(width, true, true)
}

func (m Model) treeViewSides(width int, leftB, rightB bool) string {
	focused := m.active == treePane
	title := "Lists"
	if len(m.flat) > 0 {
		title += mutedSty.Render("  " + fmt.Sprintf("%d", len(m.flat)))
	}
	if sel := m.selectedCount(); sel > 0 {
		title += "  " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fmt.Sprintf("☑ %d", sel))
	}
	if m.tagFilter != "" {
		title += "  " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render("#"+m.tagFilter)
	}
	innerWidth := max(1, width)
	if leftB {
		innerWidth--
	}
	if rightB {
		innerWidth--
	}
	var lines []string
	rows := m.treeRows()
	end := min(len(m.flat), m.treeOffset+rows)
	accentText := lipgloss.NewStyle().Foreground(accent)
	for i := m.treeOffset; i < end; i++ {
		item := m.flat[i]
		indent := strings.Repeat("  ", item.depth)
		textName := lipgloss.NewStyle().Foreground(text).Bold(true)
		var label string
		if item.node.IsDir {
			marker := "▸ "
			if m.expanded[item.node.RelPath] {
				marker = "▾ "
			}
			label = accentText.Render(marker) + textName.Render(item.node.Name)
		} else {
			mark := "○ "
			if m.selectedItems[item.node.RelPath] {
				mark = "☑ "
			}
			name := strings.TrimSuffix(item.node.Name, filepath.Ext(item.node.Name))
			label = mark + mutedSty.Render(name)
			if m.nodePinned[item.node.RelPath] {
				label = mark + accentText.Render("★ ") + mutedSty.Render(name)
			}
			if count := len(m.nodeTags[item.node.RelPath]); count > 0 {
				label = label + " " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fmt.Sprintf("#%d", count))
			}
		}
		row := " " + indent + label
		if i == m.selected {
			row = accentText.Render("▸ ") + indent + label
			row = truncateANSI(row, innerWidth)
			row = lipgloss.NewStyle().Background(selection).Foreground(text).Bold(true).Width(innerWidth).Render(row)
		} else {
			row = truncateANSI(row, innerWidth)
		}
		lines = append(lines, row)
	}
	if len(m.flat) == 0 {
		lines = append(lines,
			"  "+mutedSty.Render("No notes yet. Press n to create one."),
			"",
			"  "+accentText.Render("n")+mutedSty.Render(" new note")+"   "+accentText.Render("N")+mutedSty.Render(" new folder"),
		)
	}
	return borderedPanelPart(title, strings.Join(lines, "\n"), width, m.bodyRenderHeight(), focused, leftB, rightB)
}

func (m Model) contentView(width int) string {
	return m.contentViewSides(width, true, true)
}

func (m Model) contentViewSides(width int, leftB, rightB bool) string {
	focused := m.active == contentPane || m.mode == modeEdit
	title := "Note"
	if m.mode == modeEdit {
		title = "Markdown"
	}
	if m.currentPath != "" {
		title += " · " + filepath.Base(m.currentPath)
		if m.nodePinned[m.currentPath] {
			title += " ★"
		}
		if m.mode == modeNormal && m.previewLineCount() > m.preview.Height {
			title += " · " + lipgloss.NewStyle().Foreground(accent).Render(fmt.Sprintf("%d%% read", m.previewPercent()))
		}
	}

	innerWidth := max(1, width)
	if leftB {
		innerWidth--
	}
	if rightB {
		innerWidth--
	}

	// Metadata line (replaces old Details panel)
	var meta string
	if m.currentPath != "" {
		state := lipgloss.NewStyle().Foreground(green).Bold(true).Render("saved")
		if m.dirty() {
			state = lipgloss.NewStyle().Foreground(danger).Bold(true).Render("unsaved")
		}
		modeName := "preview"
		if m.mode == modeEdit {
			modeName = "edit"
		}
		content := m.editor.Value()
		words := wordCount(content)
		chars := charCount(content)
		lines := lineCountOf(content)
		stats := fmt.Sprintf("%d words · %d chars · %d lines · ~%s read", words, chars, lines, readingTimeEstimate(content))
		metaLine := " " + mutedSty.Render(m.currentPath) + "   " +
			state + "   " +
			mutedSty.Render(modeName) + "   " +
			mutedSty.Render(stats)
		meta = truncateANSI(metaLine, max(1, innerWidth-2)) + "\n"
		if tags := m.nodeTags[m.currentPath]; len(tags) > 0 {
			meta += " " + m.tagsRow(tags, max(4, innerWidth-2)) + "\n"
		}
	}

	var content string
	if m.currentPath == "" {
		content = "\n" + mutedSty.Render("  Select a note from the tree, or press n to create one.")
	} else if m.mode == modeEdit {
		content = m.editor.View()
	} else {
		content = m.preview.View()
		if m.contentDragging {
			// While a drag selection is in progress, render the selected
			// span highlighted. The selection offsets are rune offsets into
			// the plain text, so map them onto the ANSI-rendered content
			// without disturbing the viewport's scroll position (line
			// count is unchanged).
			start, end := m.contentSelAnchor, m.contentSelEnd
			if start > end {
				start, end = end, start
			}
			if end > start {
				highlighted := renderSelectionContent(m.renderedContent, m.renderedPlain, start, end)
				m.preview.SetContent(highlighted)
				content = m.preview.View()
			}
		}
	}

	return borderedPanelPart(title, meta+content, width, m.bodyRenderHeight(), focused, leftB, rightB)
}

func (m Model) contentHeight() int {
	if m.compact || m.bodyHeight < 12 {
		return max(1, m.bodyHeight-3)
	}
	return max(6, m.bodyHeight-3)
}

func (m Model) detailsView(width int) string {
	if m.currentPath == "" {
		return mutedSty.Render(" No note selected")
	}
	state := lipgloss.NewStyle().Foreground(green).Render("saved")
	if m.dirty() {
		state = lipgloss.NewStyle().Foreground(danger).Render("unsaved")
	}
	modeName := "preview"
	if m.mode == modeEdit {
		modeName = "edit"
	}
	content := m.editor.Value()
	words := len(strings.Fields(content))
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}
	line1 := " path  " + truncate(m.currentPath, max(1, width-8))
	line2 := " state " + state + mutedSty.Render("   mode ") + modeName + mutedSty.Render("   words ") + fmt.Sprintf("%d", words) + mutedSty.Render("   lines ") + fmt.Sprintf("%d", lines)
	return line1 + "\n" + line2
}

func borderedPanel(title, content string, width, height int, focused bool) string {
	return borderedPanelPart(title, content, width, height, focused, true, true)
}

func borderedPanelPart(title, content string, width, height int, focused bool, leftB, rightB bool) string {
	width, height = max(4, width), max(3, height)
	borderColor := muted
	titleColor := muted
	if focused {
		borderColor = accent
		titleColor = accent
	}
	leftOffset, rightOffset := 0, 0
	if leftB {
		leftOffset = 1
	}
	if rightB {
		rightOffset = 1
	}
	innerWidth := max(1, width-leftOffset-rightOffset)
	innerHeight := max(1, height-2)
	title = truncate(title, max(1, innerWidth-2))
	titleText := lipgloss.NewStyle().Foreground(titleColor).Bold(focused).Render(" " + title + " ")
	topTail := max(0, innerWidth-lipgloss.Width(titleText))
	top := ""
	if leftB {
		top += "┌"
	}
	top += titleText + strings.Repeat("─", topTail)
	if rightB {
		top += "┐"
	}

	content = fitBlock(content, innerWidth, innerHeight)
	rows := strings.Split(content, "\n")
	border := lipgloss.NewStyle().Foreground(borderColor)
	for i, row := range rows {
		line := ""
		if leftB {
			line += border.Render("│")
		}
		line += truncateANSI(row, innerWidth) + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(row)))
		if rightB {
			line += border.Render("│")
		}
		rows[i] = line
	}
	bottom := ""
	if leftB {
		bottom += "└"
	}
	bottom += strings.Repeat("─", innerWidth)
	if rightB {
		bottom += "┘"
	}
	return top + "\n" + strings.Join(rows, "\n") + "\n" + bottom
}

func fitBlock(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = truncateANSI(line, width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) statusView() string {
	if m.selecting {
		return lipgloss.NewStyle().Foreground(accent).Width(m.width).MaxHeight(1).Render(" Select text now · terminal may auto-copy · press any key to return")
	}
	if m.mode == modeEdit {
		return m.editShortcutBar()
	}
	return m.shortcutBar()
}

func (m Model) toolbarShortcut(budget int) string {
	var b strings.Builder
	for _, item := range m.toolbarItems() {
		key, label, _ := strings.Cut(item.label, " ")
		chunk := "[" + key + "] " + label + "  "
		if lipgloss.Width(b.String())+lipgloss.Width(chunk) > budget {
			break
		}
		b.WriteString(mutedSty.Render("[" + key + "]"))
		b.WriteString(" " + mutedSty.Render(label) + "  ")
	}
	return b.String()
}

func (m Model) toolbarBudget() int {
	right := m.statusText(m.status)
	if right == "" {
		right = m.readingStatus()
	}
	return max(0, m.width-lipgloss.Width(right)-4)
}

func (m Model) shortcutBar() string {
	if m.mode == modeEdit {
		return m.editShortcutBar()
	}
	right := m.statusText(m.status)
	if right == "" {
		right = m.readingStatus()
	}
	return m.composeBar(m.toolbarShortcut(m.toolbarBudget()), right)
}

func (m Model) editShortcutBar() string {
	undoSty := mutedSty
	if m.undoable() {
		undoSty = lipgloss.NewStyle().Foreground(accent)
	}
	redoSty := mutedSty
	if m.redoable() {
		redoSty = lipgloss.NewStyle().Foreground(accent)
	}
	left := mutedSty.Render("Ctrl+S · ") +
		undoSty.Render("Ctrl+Z") +
		mutedSty.Render(" · ") +
		redoSty.Render("Ctrl+Shift+Z") +
		mutedSty.Render(" · Esc · Ctrl+L")
	pos := m.cursorPos()
	total := m.editor.LineCount()
	right := fmt.Sprintf("Line %d / %d · Column %d", pos.row+1, total, pos.col+1)
	return m.composeBar(left, right)
}

func (m Model) composeBar(left, right string) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	divider := ""
	if lw > 0 && rw > 0 {
		divider = mutedSty.Render("│") + " "
	}
	dw := lipgloss.Width(divider)
	pad := m.width - lw - rw - dw - 2
	if pad < 0 {
		pad = 0
	}
	return lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render(" " + left + divider + strings.Repeat(" ", pad) + " " + right)
}

func (m Model) searchBarView() string {
	return lipgloss.NewStyle().Background(surface).Foreground(text).Width(m.width).MaxHeight(1).Render(" " + m.input.View())
}

func (m Model) searchStatusView() string {
	left := mutedSty.Render("Next: Enter · Prev: Shift+Enter · Close: Esc")
	var right string
	if len(m.searchMatches) == 0 {
		right = errorSty.Render("No matches")
	} else {
		right = statusSty.Render(fmt.Sprintf("%d/%d matches", m.searchIndex+1, len(m.searchMatches)))
	}
	return m.composeBar(left, right)
}

func (m Model) gotoBarView() string {
	return lipgloss.NewStyle().Background(surface).Foreground(text).Width(m.width).MaxHeight(1).Render(" " + m.input.View())
}

func (m Model) statusText(status string) string {
	if m.statusErr {
		return errorSty.Render(status)
	}
	if m.statusOK {
		return successSty.Render(status)
	}
	return statusSty.Render(status)
}

func (m Model) dialogView() string {
	var title, body string
	if m.mode == modeConfirm {
		if m.confirmCount > 0 {
			title = "Delete notes"
			body = fmt.Sprintf("将删除 %d 个笔记", m.confirmCount)
		} else if m.confirmDir {
			title = "Delete folder"
			body = "Delete “" + m.confirm + "” and all contents?"
		} else {
			title = "Delete note"
			body = "Delete “" + m.confirm + "”?"
		}
		body += "\n\n" + dangerStyle("Enter / Y  确认删除") + "    " + mutedSty.Render("Esc / N  取消")
	} else {
		switch m.promptKind {
		case promptNote:
			title = "New note"
		case promptDir:
			title = "New folder"
		case promptRename:
			title = "Rename"
		}
		body = m.input.View() + "\n\n" + mutedSty.Render("Enter confirm  ·  Esc cancel")
		if m.statusErr {
			body += "\n" + errorSty.Render(m.status)
		}
	}
	dialog := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(64, max(28, m.width-6))).Render(brandSty.Render(title) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func (m *Model) startHelp() {
	m.beforeHelp = m.mode
	m.mode = modeHelp
	m.helpHintQ = ""
	m.input.Prompt = "Search: "
	m.input.Placeholder = "filter shortcuts"
	m.input.SetValue("")
	m.input.Width = max(10, min(50, m.width-12))
	m.input.Focus()
	m.renderHelpContent()
}

func (m Model) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		evt := tea.MouseEvent(msg)
		if evt.IsWheel() {
			updated, _ := m.helpHintView.Update(tea.MouseMsg(msg))
			m.helpHintView = updated
		}
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "esc" || key == "?" || key == "enter" {
			m.mode = m.beforeHelp
			m.input.Blur()
			m.input.Prompt = "› "
			m.helpHintQ = ""
			m.renderHelpContent()
			m.restoreEditFocus()
			return m, nil
		}
		switch msg.Type {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			updated, _ := m.helpHintView.Update(msg)
			m.helpHintView = updated
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if q := m.input.Value(); q != m.helpHintQ {
			m.helpHintQ = q
			m.renderHelpContent()
		}
		return m, cmd
	}
	return m, nil
}

func (m *Model) renderHelpContent() {
	w := m.helpBoxWidth()
	m.helpHintView.Width = max(10, w-6)
	m.helpHintView.Height = max(3, m.height-6)
	m.helpHintView.SetContent(m.helpContent())
}

func (m Model) helpBoxWidth() int {
	return min(82, max(28, m.width-4))
}

func (m Model) helpContent() string {
	var b strings.Builder
	q := strings.ToLower(strings.TrimSpace(m.helpHintQ))
	total := 0
	for _, g := range helpGroupsData {
		var rows []helpRow
		for _, r := range g.rows {
			if q == "" || strings.Contains(strings.ToLower(r.keys), q) || strings.Contains(strings.ToLower(r.desc), q) {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		maxKey := 0
		for _, r := range rows {
			if w := lipgloss.Width(r.keys); w > maxKey {
				maxKey = w
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Render(g.title) + "\n")
		for _, r := range rows {
			total++
			pad := maxKey - lipgloss.Width(r.keys) + 2
			b.WriteString("  " + lipgloss.NewStyle().Foreground(accent).Render(r.keys) + strings.Repeat(" ", pad) + mutedSty.Render(r.desc) + "\n")
		}
		b.WriteString("\n")
	}
	if q != "" && total == 0 {
		b.WriteString(errorSty.Render("No matching shortcuts"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("TN shortcuts") + "\n\n")
	b.WriteString("  " + m.input.View() + "\n\n")
	b.WriteString(m.helpHintView.View())
	b.WriteString("\n\n" + mutedSty.Render("↑/↓ / mouse scroll · Esc close"))
	box := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 2).Width(m.helpBoxWidth()).Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg))
}

func (m *Model) startCommand() {
	m.commandBeforeMode = m.mode
	m.commandBeforeActive = m.active
	m.mode = modeCommand
	m.commandIndex = 0
	m.commandQuery = ""
	m.input.Prompt = "Commands: "
	m.input.Placeholder = "type to filter"
	m.input.SetValue("")
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
	m.statusErr = false
	m.status = ""
}

// startWebdavConfig opens the WebDAV configuration dialog.
func (m *Model) startWebdavConfig() {
	m.mode = modeWebdavConfig
	m.input.Prompt = "WebDAV URL: "
	m.input.Placeholder = "https://example.com/dav"
	config := loadWebDAVConfig(m.store.Root)
	m.input.SetValue(config.URL)
	m.input.Width = max(20, min(80, m.width-12))
	m.input.Focus()
	m.statusErr = false
	m.status = ""
	m.webdavInputStep = 0
	m.webdavConfig = config
}

// syncWebdavNow triggers immediate sync.
func (m *Model) syncWebdavNow() {
	m.mode = modeWebdavSync
	m.status = "Syncing…"
	stats := m.doWebDAVSync()
	_ = stats
}

// webdavInputStep tracks which field we're editing.
// 0=URL, 1=username, 2=password, 3=remote_path, 4=auto_sync_minutes.

// updateWebdavConfig handles input during WebDAV config mode.
func (m *Model) updateWebdavConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		return m, nil
	case "enter":
		return m.advanceWebdavConfig()
	case "tab":
		return m.advanceWebdavConfig()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// advanceWebdavConfig saves the current field and advances to next.
func (m *Model) advanceWebdavConfig() (tea.Model, tea.Cmd) {
	val := strings.TrimSpace(m.input.Value())
	switch m.webdavInputStep {
	case 0:
		m.webdavConfig.URL = val
		m.webdavConfig.SyncEnabled = val != ""
		m.input.Prompt = "Username: "
		m.input.Placeholder = "user"
		m.input.SetValue(m.webdavConfig.Username)
	case 1:
		m.webdavConfig.Username = val
		m.input.Prompt = "Password: "
		m.input.Placeholder = "password"
		m.input.SetValue(m.webdavConfig.Password)
	case 2:
		m.webdavConfig.Password = val
		m.input.Prompt = "Remote path: "
		m.input.Placeholder = "/tn"
		m.input.SetValue(m.webdavConfig.RemotePath)
	case 3:
		m.webdavConfig.RemotePath = val
		if val == "" {
			m.webdavConfig.RemotePath = "/tn"
		}
		m.input.Prompt = "Auto-sync mins (0=manual): "
		m.input.Placeholder = "0"
		m.input.SetValue(fmt.Sprintf("%d", m.webdavConfig.AutoSyncMins))
	case 4:
		autoSync := 0
		fmt.Sscanf(val, "%d", &autoSync)
		m.webdavConfig.AutoSyncMins = autoSync
		// Save config
		if err := saveWebDAVConfig(m.store.Root, m.webdavConfig); err != nil {
			m.flashStatus("Save failed: "+err.Error(), true, 2*time.Second)
		} else {
			m.flashStatus("✓ WebDAV config saved", false, 2*time.Second)
		}
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		m.webdavInputStep = 0
		return m, m.takePending()
	}
	m.webdavInputStep++
	m.input.Width = max(20, min(80, m.width-12))
	m.input.Focus()
	return m, nil
}

// webdavConfigView renders the WebDAV config form.
func (m *Model) webdavConfigView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("WebDAV sync settings") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	b.WriteString(mutedSty.Render("Tab/Enter next · Esc cancel"))
	dialog := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(80, max(40, m.width-6))).Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func (m *Model) updateCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitCommand()
		return *m, nil
	case "enter":
		cmds := m.filteredCommands()
		if len(cmds) > 0 && m.commandIndex >= 0 && m.commandIndex < len(cmds) {
			action := cmds[m.commandIndex].action
			m.exitCommand()
			action()
			return *m, m.takePending()
		}
		return *m, nil
	case "up":
		m.moveCommand(-1)
		return *m, nil
	case "down":
		m.moveCommand(1)
		return *m, nil
	case "home":
		m.commandIndex = 0
		return *m, nil
	case "end":
		m.commandIndex = max(0, len(m.filteredCommands())-1)
		return *m, nil
	case "pgup":
		m.moveCommand(-10)
		return *m, nil
	case "pgdown":
		m.moveCommand(10)
		return *m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if q := m.input.Value(); q != m.commandQuery {
		m.commandQuery = q
		m.commandIndex = 0
	}
	return *m, cmd
}

func (m *Model) exitCommand() {
	m.mode = m.commandBeforeMode
	m.active = m.commandBeforeActive
	m.input.Blur()
	m.input.Prompt = "› "
	m.commandQuery = ""
	m.commandIndex = 0
	m.restoreCommandFocus()
}

func (m *Model) restoreCommandFocus() {
	if m.mode == modeEdit {
		m.active = contentPane
		m.setEditorBackground(surface)
		m.editor.Focus()
	} else {
		m.active = m.commandBeforeActive
		m.setEditorBackground(bg)
	}
}

func (m *Model) commandList() []command {
	return []command{
		{"Save note", func() { m.save() }},
		{"Undo", m.undo},
		{"Redo", m.redo},
		{"Toggle edit/preview", m.toggleEdit},
		{"Find in note", m.findInNoteCommand},
		{"Find everywhere", m.startGlobalSearch},
		{"Quick open", m.startQuickOpen},
		{"Go to line", m.startGotoLine},
		{"Focus mode", m.toggleFocus},
		{"Toggle tree", m.toggleTree},
		{"Export note", m.exportNoteCommand},
		{"Export as HTML", func() {
			if m.currentPath == "" {
				m.flashStatus("Open a note first", true, 2*time.Second)
				return
			}
			m.startHTMLExport()
		}},
		{"New note", func() { m.startPrompt(promptNote) }},
		{"New folder", func() { m.startPrompt(promptDir) }},
		{"Rename", func() { m.startPrompt(promptRename) }},
		{"Delete", m.startDelete},
		{"Toggle tag filter", m.startTagFilter},
		{"Clean up unused images", func() {
			m.cleanupImages()
		}},
		{"WebDAV sync settings", func() { m.startWebdavConfig() }},
		{"Sync now", func() { m.syncWebdavNow() }},
	}
}

func (m *Model) filteredCommands() []command {
	q := strings.ToLower(strings.TrimSpace(m.commandQuery))
	var out []command
	for _, c := range m.commandList() {
		if q == "" || strings.Contains(strings.ToLower(c.name), q) {
			out = append(out, c)
		}
	}
	return out
}

func (m *Model) moveCommand(delta int) {
	n := len(m.filteredCommands())
	if n == 0 {
		return
	}
	m.commandIndex = ((m.commandIndex+delta)%n + n) % n
}

func (m *Model) findInNoteCommand() {
	if m.currentPath == "" {
		m.flashStatus("Open a note first", true, 2*time.Second)
		return
	}
	m.startSearch()
}

func (m *Model) exportNoteCommand() {
	if m.currentPath == "" {
		m.flashStatus("Open a note first", true, 2*time.Second)
		return
	}
	m.startExport()
}

func (m Model) commandView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Commands") + "\n\n")
	b.WriteString(m.input.View() + "\n")
	cmds := m.filteredCommands()
	if len(cmds) == 0 {
		b.WriteString("\n" + mutedSty.Render("No matching commands"))
	} else {
		for i, c := range cmds {
			if i >= 15 {
				break
			}
			row := "  " + c.name
			if i == m.commandIndex {
				row = lipgloss.NewStyle().Background(selection).Foreground(text).Bold(true).Render("▸ " + c.name)
			}
			b.WriteString("\n" + row)
		}
	}
	b.WriteString("\n\n" + mutedSty.Render("Type to filter commands · ↑/↓ select · Enter run · Esc close"))
	return m.bottomOverlay(b.String())
}

func wordCount(s string) int {
	count := len(strings.Fields(s))
	for _, r := range s {
		if isCJK(r) {
			count++
		}
	}
	return count
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x30FF)
}

func readingTimeEstimate(s string) string {
	words := wordCount(s)
	if words < 200 {
		return "<1 min"
	}
	return fmt.Sprintf("%d min", (words+199)/200)
}

func charCount(s string) int {
	return utf8.RuneCountInString(s)
}

func lineCountOf(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func (m Model) previewPercent() int {
	return max(0, min(100, int(math.Round(m.preview.ScrollPercent()*100))))
}

func progressBar(percent int) string {
	const width = 8
	pct := max(0, min(100, percent))
	filled := int(math.Round(float64(pct) / 100 * width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("▰", filled)) +
		mutedSty.Render(strings.Repeat("▱", width-filled))
}

func (m Model) readingStatus() string {
	if m.currentPath == "" {
		return mutedSty.Render("TN · select a note from the tree, or press n to create one")
	}
	var parts []string
	if m.mode == modeNormal && m.previewLineCount() > m.preview.Height {
		pct := m.previewPercent()
		parts = append(parts, progressBar(pct)+" "+fmt.Sprintf("%d%%", pct))
	}
	if m.nodePinned[m.currentPath] {
		parts = append(parts, lipgloss.NewStyle().Foreground(accent).Render("★ pinned"))
	}
	return strings.Join(parts, "  ")
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func truncateANSI(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return termansi.Truncate(s, max(1, width), "…")
}

func dangerStyle(s string) string {
	return lipgloss.NewStyle().Foreground(danger).Bold(true).Render(s)
}
