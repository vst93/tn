package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

	"github.com/vst93/vnote/internal/storage"
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
	modeTag
	modeTagFilter
	modeTemplate
)

type promptKind int

const (
	promptNote promptKind = iota
	promptDir
	promptRename
	promptGotoLine
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
}

var noteTemplates = map[string]string{
	"blank":   "# {{title}}\n\n",
	"daily":   "## 今日完成\n\n\n## 明日计划\n\n\n## 问题/阻塞\n\n",
	"meeting": "## 参会人\n\n\n## 议题\n\n\n## 结论\n\n\n## 待办\n\n",
	"book":    "## 书名\n\n\n## 作者\n\n\n## 核心观点\n\n\n## 摘录\n\n\n## 我的思考\n\n",
}

var noteTemplateNames = []string{"blank", "daily", "meeting", "book"}

var noteTemplateLabels = map[string]string{
	"blank":   "空白笔记",
	"daily":   "日报",
	"meeting": "会议记录",
	"book":    "读书笔记",
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

type toolbarItem struct{ label, action string }

type copyResultMsg struct {
	err     error
	content string
}
type selectionModeMsg struct{}
type statusClearMsg struct{ id uint64 }

type cursorPos struct{ row, col int }

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
}

type globalSearchResult struct {
	path    string
	title   string
	snippet string
	lineNum int
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

// Model is the Vnote Bubble Tea application.
type Model struct {
	store *storage.Store

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
	undoStack       []string
	redoStack       []string
	input           textinput.Model
	promptKind      promptKind
	beforePrompt    mode
	exportPath      bool
	exportCopy      bool

	searchQuery   string
	searchMatches []matchPos
	searchIndex   int

	width, height int
	treeWidth     int
	bodyHeight    int
	compact       bool
	selecting     bool
	copier        func(string) error
	pending       tea.Cmd

	focusing            bool
	sessionPath         string
	beforeGlobalSearch  mode
	globalSearchQuery   string
	globalSearchResults []globalSearchResult
	globalSearchIndex   int

	tagFilter       string
	nodeTags        map[string][]string
	templateIndex   int
	pendingTemplate string

	renderer      *glamour.TermRenderer
	rendererWidth int

	status     string
	statusErr  bool
	statusOK   bool
	statusID   uint64
	statusCmd  tea.Cmd
	confirm    string
	confirmDir bool
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
		store:       store,
		expanded:    make(map[string]bool),
		nodeTags:    make(map[string][]string),
		active:      treePane,
		mode:        modeNormal,
		editor:      editor,
		preview:     viewport.New(60, 20),
		input:       input,
		copier:      copyText,
		status:      "Ready",
		sessionPath: defaultSessionPath(),
	}
	m.preview.MouseWheelEnabled = true
	m.preview.MouseWheelDelta = 3
	m.refresh("")
	m = m.restoreSession()
	return m
}

func defaultSessionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vnote", "session.json")
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

func (m Model) Init() tea.Cmd { return textarea.Blink }

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
		if key == "ctrl+c" {
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
			if key == "?" || key == "esc" || key == "enter" {
				m.mode = m.beforeHelp
			}
			return m, nil
		}
		if m.mode == modeSearch {
			return m.updateSearch(msg)
		}
		if m.mode == modeSearchGlobal {
			return m.updateGlobalSearch(msg)
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
		if m.mode == modeTemplate {
			return m.updateTemplate(msg)
		}

		if handled, quit := m.globalKey(key); handled {
			if quit {
				return m, tea.Quit
			}
			return m, m.takePending()
		}

		if m.mode == modeEdit {
			before := m.editor.Value()
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
					if m.handleEditEnter(before) {
						m.editSel = nil
						return m, nil
					}
				}
				m.editSel = nil
				m.editor, cmd = m.editor.Update(msg)
			}
			m.recordEdit(before, m.editor.Value())
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
		if m.currentPath == "" {
			m.setStatus("Open a note first", true)
			return true, false
		}
		m.startExport()
		return true, false
	case "ctrl+n":
		m.startTemplate()
		return true, false
	case "ctrl+d":
		m.startPrompt(promptDir)
		return true, false
	case "f2":
		m.startPrompt(promptRename)
		return true, false
	case "delete", "ctrl+backspace", "x":
		m.startDelete()
		return true, false
	case "r":
		m.startPrompt(promptRename)
		return true, false
	case "n":
		m.startTemplate()
		return true, false
	case "d":
		m.startPrompt(promptDir)
		return true, false
	case "e":
		m.toggleEdit()
		return true, false
	case "s":
		m.save()
		return true, false
	case "y":
		m.copyCurrent()
		return true, false
	case "g":
		m.enterSelectionMode()
		return true, false
	case "q":
		if m.dirty() {
			m.setStatus("Save or discard changes before quitting", true)
			return true, false
		}
		m.saveSession()
		return true, true
	case "ctrl+shift+o":
		m.startGlobalSearch()
		return true, false
	case "ctrl+shift+f":
		m.toggleFocus()
		return true, false
	case "ctrl+shift+t":
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
	case "ctrl+f":
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
		m.enterSelectionMode()
		return true, false
	case "ctrl+e":
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
	case "?":
		m.beforeHelp = m.mode
		m.mode = modeHelp
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
	case "right", "l", "enter", " ":
		m.activateSelected()
	case "left", "h":
		m.collapseOrParent()
	}
}

func (m *Model) handleMouse(msg tea.MouseEvent) {
	if msg.Action != tea.MouseActionPress && !msg.IsWheel() {
		return
	}
	if msg.Y == 0 && msg.Button == tea.MouseButtonLeft {
		if msg.X >= 10 && msg.X < 20 {
			m.switchToTree()
		} else if msg.X >= 20 && msg.X < 34 {
			m.switchToContent()
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

	inTree := !m.compact && msg.X < m.treeWidth
	if m.compact {
		inTree = m.active == treePane
	}
	if inTree {
		m.switchToTree()
		if msg.Button == tea.MouseButtonWheelUp {
			m.treeOffset = max(0, m.treeOffset-3)
			return
		}
		if msg.Button == tea.MouseButtonWheelDown {
			m.treeOffset = min(max(0, len(m.flat)-m.treeRows()), m.treeOffset+3)
			return
		}
		if msg.Button == tea.MouseButtonLeft {
			row := msg.Y - 2 + m.treeOffset
			if row >= 0 && row < len(m.flat) {
				m.selected = row
				if m.flat[row].node.IsDir {
					m.activateSelected()
				} else {
					m.openSelectedNote()
				}
			}
		}
		return
	}

	m.switchToContent()
	if m.mode == modeNormal && msg.IsWheel() {
		updated, _ := m.preview.Update(tea.MouseMsg(msg))
		m.preview = updated
	}
}

func (m *Model) runAction(action string) {
	switch action {
	case "note":
		m.startTemplate()
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
	case "select":
		m.enterSelectionMode()
	case "save":
		m.save()
	case "pane":
		m.togglePane()
	case "help":
		m.beforeHelp = m.mode
		m.mode = modeHelp
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
	if len([]rune(content)) == 0 {
		return "✓ Copied empty note"
	}
	display := strings.Join(strings.Fields(content), " ")
	n := len([]rune(content))
	if n <= 40 {
		return "✓ Copied: " + display
	}
	if n <= 120 {
		return "✓ Copied: " + truncate(display, 40) + "..."
	}
	return fmt.Sprintf("✓ Copied %d chars", n)
}

func (m *Model) startExport() {
	m.mode = modeExport
	m.exportPath = false
	m.exportCopy = false
	m.statusErr = false
	m.status = ""
	m.input.Prompt = "Export to: "
	m.input.Placeholder = "filename or path"
	m.input.SetValue(m.defaultExportPath())
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Blur()
}

func (m Model) updateExport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.exportPath {
		switch msg.String() {
		case "esc":
			m.mode = modeNormal
			m.input.Blur()
			return m, nil
		case "1":
			m.doExportCopy()
			return m, m.takePending()
		case "2":
			m.exportPath = true
			m.statusErr = false
			m.status = ""
			m.input.SetValue(m.defaultExportPath())
			m.input.Focus()
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.exportPath = false
		m.input.Blur()
		return m, nil
	case "enter":
		m.performSaveAs()
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
	if err := os.WriteFile(path, []byte(m.editor.Value()), 0o644); err != nil {
		m.setStatus("Export failed: "+err.Error(), true)
		return
	}
	m.mode = modeNormal
	m.exportPath = false
	m.input.Blur()
	m.flashStatus("✓ Exported to "+path, false, 2*time.Second)
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

func (m Model) exportDialogView() string {
	title := "Export"
	var body string
	if m.exportPath {
		body = m.input.View() + "\n\n" + mutedSty.Render("Enter 导出  ·  Esc 取消")
		if m.statusErr {
			body += "\n" + errorSty.Render(m.status)
		}
	} else {
		body = "1  复制到剪贴板\n2  另存为\n\n" + mutedSty.Render("Esc 取消")
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

func (m *Model) recordEdit(before, after string) {
	if before == after {
		return
	}
	m.undoStack = append(m.undoStack, before)
	m.redoStack = m.redoStack[:0]
}

func (m *Model) undo() {
	if m.mode != modeEdit || len(m.undoStack) == 0 {
		return
	}
	m.redoStack = append(m.redoStack, m.editor.Value())
	prev := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.editor.SetValue(prev)
	m.editSel = nil
	m.editor.SetCursor(0)
}

func (m *Model) redo() {
	if m.mode != modeEdit || len(m.redoStack) == 0 {
		return
	}
	m.undoStack = append(m.undoStack, m.editor.Value())
	next := m.redoStack[len(m.redoStack)-1]
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.editor.SetValue(next)
	m.editSel = nil
	m.editor.SetCursor(0)
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
	} else {
		m.switchToTree()
	}
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
	if m.dirty() && !m.save() {
		return
	}
	content, err := m.store.Read(path)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.currentPath = path
	m.original = content
	m.editor.SetValue(content)
	m.undoStack = nil
	m.redoStack = nil
	m.editSel = nil
	m.setEditorBackground(bg)
	m.preview.GotoTop()
	m.renderMarkdown()
	m.flashStatus("Opened "+path, false, 2*time.Second)
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
		m.input.Placeholder = "note name"
	case promptDir:
		m.input.Placeholder = "folder name"
	case promptRename:
		m.input.Placeholder = "new name"
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
		var path string
		var err error
		switch m.promptKind {
		case promptNote:
			path, err = m.store.CreateNote(m.selectedParent(), value)
			if err == nil && m.pendingTemplate != "" {
				if tmpl, ok := noteTemplates[m.pendingTemplate]; ok {
					title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
					_ = m.store.Write(path, strings.ReplaceAll(tmpl, "{{title}}", title))
				}
				m.pendingTemplate = ""
			}
		case promptDir:
			path, err = m.store.CreateDir(m.selectedParent(), value)
		case promptRename:
			path, err = m.renameSelected(value)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
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

func (m *Model) startTemplate() {
	m.templateIndex = 0
	m.pendingTemplate = ""
	m.mode = modeTemplate
	m.statusErr = false
	m.status = ""
}

func (m Model) updateTemplate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "1", "2", "3", "4":
		idx, _ := strconv.Atoi(msg.String())
		m.templateIndex = idx - 1
		return m, nil
	case "enter":
		if m.templateIndex < 0 || m.templateIndex >= len(noteTemplateNames) {
			m.setStatus("Select a template first", true)
			return m, nil
		}
		m.pendingTemplate = noteTemplateNames[m.templateIndex]
		m.mode = modeNormal
		m.startPrompt(promptNote)
		return m, nil
	}
	return m, nil
}

func (m Model) templateView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("选择模板") + "\n\n")
	for i, key := range noteTemplateNames {
		marker := "  "
		if i == m.templateIndex {
			marker = "▸ "
		}
		b.WriteString(marker + fmt.Sprintf("%d. %s\n", i+1, noteTemplateLabels[key]))
	}
	b.WriteString("\n" + mutedSty.Render("1-4 选择 · Enter 确认 · Esc 取消"))
	return m.bottomOverlay(b.String())
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

func (m Model) updateTagEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		m.input.Blur()
		m.input.Prompt = "› "
		m.editor.Focus()
		return m, nil
	case "enter":
		m.saveTags(m.input.Value())
		if m.mode == modeTag {
			return m, nil
		}
		m.input.Blur()
		m.input.Prompt = "› "
		m.editor.Focus()
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
	m.rebuildFlat()
	m.flashStatus("Tags saved", false, 2*time.Second)
}

func (m Model) tagEditView() string {
	title := "Edit tags"
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
	return lipgloss.NewStyle().Background(surface).Foreground(text).Width(m.width).MaxHeight(1).Render(" # " + m.input.View())
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
	return newPath, nil
}

func (m *Model) startDelete() {
	if len(m.flat) == 0 {
		m.flashStatus("Nothing selected", true, 2*time.Second)
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
		m.mode = modeNormal
	case "n", "N", "esc":
		m.mode = modeNormal
		m.flashStatus("Delete cancelled", false, 2*time.Second)
	}
	return m, m.takePending()
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
	if m.tagFilter != "" {
		m.rebuildFlat()
	}
	m.renderMarkdown()
	m.flashStatus("✓ Saved "+m.currentPath, false, 2*time.Second)
	return true
}

func (m *Model) dirty() bool {
	return m.currentPath != "" && m.editor.Value() != m.original
}

func (m *Model) lineCount() int {
	return m.editor.LineCount()
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
	return topLine + "\n" + body + "\n" + bottomLine
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
					results = append(results, globalSearchResult{path: n.RelPath, title: title, snippet: strings.TrimSpace(line), lineNum: li + 1})
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
	if m.dirty() && !m.save() {
		return
	}
	content, err := m.store.Read(r.path)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.currentPath = r.path
	m.original = content
	m.editor.SetValue(content)
	m.undoStack = nil
	m.redoStack = nil
	m.editSel = nil
	m.expandParents(r.path)
	m.rebuildFlat()
	m.selectPath(r.path)
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
	b.WriteString("\n\n" + mutedSty.Render("↑/↓ select · Enter open · Esc cancel"))
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
	snippet := highlightKeyword(r.snippet, m.globalSearchQuery)
	row := title + "   " + snippet
	if selected {
		row = lipgloss.NewStyle().Background(selection).Foreground(text).Render(row)
	}
	return row
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
	m.input.Placeholder = "line number"
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

func (m *Model) handleEditEnter(before string) bool {
	pos := m.cursorPos()
	line := m.currentLineText()
	if pos.col < len([]rune(line)) {
		return false
	}
	if m.handleListEnter() {
		m.recordEdit(before, m.editor.Value())
		return true
	}
	if indent := leadingIndent(line); indent != "" {
		m.editor.InsertString("\n" + indent)
		m.recordEdit(before, m.editor.Value())
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
		for _, n := range nodes {
			if n.IsDir {
				m.flat = append(m.flat, flatNode{node: n, depth: depth})
				if m.expanded[n.RelPath] {
					walk(n.Children, depth+1)
				}
				continue
			}
			tags := m.tagsForNode(n)
			m.nodeTags[n.RelPath] = tags
			if filter != "" && !containsTag(tags, filter) {
				continue
			}
			m.flat = append(m.flat, flatNode{node: n, depth: depth})
		}
	}
	walk(m.tree, 0)
}

func (m *Model) tagsForNode(n *storage.Node) []string {
	content, err := m.store.Read(n.RelPath)
	if err != nil {
		return nil
	}
	meta, _ := parseFrontMatter(content)
	return meta.Tags
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
	if !m.compact {
		contentWidth = max(1, m.width-m.treeWidth-1)
	}
	m.editor.SetWidth(max(10, contentWidth-4))
	m.editor.SetHeight(max(1, m.contentHeight()-2))
	m.preview.Width = max(10, contentWidth-4)
	m.adjustPreviewHeight()
	m.ensureSelectionVisible()
	m.renderMarkdown()
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
	rendered, err := m.renderer.Render(m.editor.Value())
	if err != nil {
		m.setStatus("Markdown preview: "+err.Error(), true)
		m.preview.SetContent(m.editor.Value())
		m.renderedContent = m.editor.Value()
		m.renderedPlain = m.editor.Value()
		return
	}
	content := strings.TrimSpace(decorateCodeBlocks(rendered, width))
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

func highlightSearchContent(content, query string) string {
	if query == "" {
		return content
	}
	hi := "\x1b[7m"
	hiEnd := "\x1b[27m"
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		ranges := findSearchMatches(plainTextLine(line), query)
		if len(ranges) == 0 {
			continue
		}
		lines[i] = applyHighlightRanges(line, ranges, hi, hiEnd)
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
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
		if (r < 0x20 && r != '\t') || r == 0x7f {
			b.WriteRune(r)
			i += size
			continue
		}
		if startAt[vis] {
			b.WriteString(hi)
		}
		b.WriteRune(r)
		vis++
		if endAt[vis] {
			b.WriteString(hiEnd)
		}
		i += size
	}
	return b.String()
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
	codeBg := termansi.Style{}.BackgroundColor(termansi.TrueColor(0x1B1E2B)).String()
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
		return "Vnote"
	}
	if m.mode == modeHelp {
		return m.helpView()
	}
	if m.mode == modeExport {
		return m.exportDialogView()
	}
	if m.mode == modeSearchGlobal {
		return m.globalSearchView()
	}
	if m.mode == modeTemplate {
		return m.templateView()
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
		if m.active == treePane {
			body = m.treeView(m.width)
		} else {
			body = m.contentView(m.width)
		}
	} else if m.width >= 100 {
		contentW := max(1, m.width-m.treeWidth-1)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.treeViewSides(m.treeWidth, true, false),
			mutedSty.Render("│"),
			m.contentViewSides(contentW, false, true),
		)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.treeView(m.treeWidth), m.contentView(m.width-m.treeWidth))
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
	brand := brandSty.Render("◆ vnote")
	tabs := m.headerTabs()
	left := brand + "  " + tabs

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

func (m Model) headerTabs() string {
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

func (m Model) toolbarItems() []toolbarItem {
	items := []toolbarItem{
		{"? help", "help"}, {"# tag", "tagfilter"}, {"n note", "note"}, {"d folder", "folder"}, {"e edit", "edit"}, {"s save", "save"}, {"y copy", "copy"}, {"g select", "select"}, {"r rename", "rename"}, {"x delete", "delete"}, {"q quit", "quit"},
	}
	if m.compact {
		label := "→ note"
		if m.active == contentPane {
			label = "← lists"
		}
		items = append(items, toolbarItem{label, "pane"})
	}
	return items
}

func (m Model) footerActionAt(x int) string {
	if m.mode == modeEdit {
		return ""
	}
	position := 1
	for _, item := range m.toolbarItems() {
		key, label, _ := strings.Cut(item.label, " ")
		width := lipgloss.Width("[" + key + "] " + label + "  ")
		if position+width > m.width {
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
	for i := m.treeOffset; i < end; i++ {
		item := m.flat[i]
		indent := strings.Repeat("  ", item.depth)
		accentText := lipgloss.NewStyle().Foreground(accent)
		textName := lipgloss.NewStyle().Foreground(text).Bold(true)
		var label string
		if item.node.IsDir {
			marker := "▸ "
			if m.expanded[item.node.RelPath] {
				marker = "▾ "
			}
			label = accentText.Render(marker) + textName.Render(item.node.Name)
		} else {
			name := strings.TrimSuffix(item.node.Name, filepath.Ext(item.node.Name))
			label = mutedSty.Render(name)
			if count := len(m.nodeTags[item.node.RelPath]); count > 0 {
				label = label + " " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fmt.Sprintf("#%d", count))
			}
		}
		row := " " + indent + label
		if i == m.selected {
			row = accentText.Render("▸") + indent + label
			row = truncateANSI(row, innerWidth)
			row = lipgloss.NewStyle().Background(selection).Foreground(text).Bold(true).Width(innerWidth).Render(row)
		} else {
			row = truncateANSI(row, innerWidth)
		}
		lines = append(lines, row)
	}
	if len(m.flat) == 0 {
		lines = append(lines, mutedSty.Render("  No notes"), mutedSty.Render("  Press n to create one"))
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
	}

	// Metadata line (replaces old Details panel)
	var meta string
	if m.currentPath != "" {
		state := lipgloss.NewStyle().Foreground(green).Render("saved")
		if m.dirty() {
			state = lipgloss.NewStyle().Foreground(danger).Render("modified")
		}
		modeName := "preview"
		if m.mode == modeEdit {
			modeName = "edit"
		}
		content := m.editor.Value()
		words := len(strings.Fields(content))
		meta = " " + mutedSty.Render("  "+m.currentPath) + "   " +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render(state) + "   " +
			mutedSty.Render(modeName) + "   " +
			mutedSty.Render(fmt.Sprintf("%d words", words)) + "\n"
		if tags := m.nodeTags[m.currentPath]; len(tags) > 0 {
			inner := max(1, width)
			if leftB {
				inner--
			}
			if rightB {
				inner--
			}
			meta += " " + m.tagsRow(tags, max(4, inner-2)) + "\n"
		}
	}

	var content string
	if m.currentPath == "" {
		content = "\n" + mutedSty.Render(" Select a note from Lists or press n to create one.")
	} else if m.mode == modeEdit {
		content = m.editor.View()
	} else {
		content = m.preview.View()
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
		state = lipgloss.NewStyle().Foreground(danger).Render("modified")
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

func (m Model) shortcutBar() string {
	if m.mode == modeEdit {
		return m.editShortcutBar()
	}
	right := ""
	if m.currentPath != "" {
		content := m.editor.Value()
		words := len(strings.Fields(content))
		lines := m.lineCount()
		minutes := (words + 199) / 200
		right = fmt.Sprintf("%d words · %d lines · ~%d min read", words, lines, minutes)
	}
	return m.composeBar(m.toolbarShortcut(m.width-lipgloss.Width(right)-2), right)
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
	left := mutedSty.Render("Ctrl+S 保存 · ") +
		undoSty.Render("Ctrl+Z 撤销") +
		mutedSty.Render(" · ") +
		redoSty.Render("Ctrl+Shift+Z 重做") +
		mutedSty.Render(" · Esc 退出 · Ctrl+L 复制行")
	pos := m.cursorPos()
	total := m.editor.LineCount()
	right := fmt.Sprintf("Ln %d / %d · Col %d", pos.row+1, total, pos.col+1)
	return m.composeBar(left, right)
}

func (m Model) composeBar(left, right string) string {
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(left)
	lw := lipgloss.Width(b.String())
	rw := lipgloss.Width(right)
	status := truncate(m.status, max(0, m.width-lw-rw-6))
	if status != "" && m.width-lw-rw > lipgloss.Width(status)+4 {
		b.WriteString("  ")
		b.WriteString(m.statusText(status))
		lw = lipgloss.Width(b.String())
	}
	pad := m.width - lw - rw - 2
	if pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	if right != "" {
		b.WriteString(" " + right)
	}
	return lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render(b.String())
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
		right = statusSty.Render(fmt.Sprintf("%d / %d matches", m.searchIndex+1, len(m.searchMatches)))
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
		if m.confirmDir {
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

func (m Model) helpView() string {
	help := brandSty.Render("Vnote shortcuts") + "\n\n" +
		"Navigate\n" + mutedSty.Render("  ↑/↓ or J/K     Select item\n  ←/→ or H/L     Collapse / expand\n  Enter           Open note\n  Tab             Switch panel\n") +
		"\nNotes\n" + mutedSty.Render("  Ctrl+N          New note (template)\n  Ctrl+D          New folder\n  F2              Rename\n  Delete          Delete\n  Ctrl+E          Edit / preview\n  Ctrl+S          Save\n  Ctrl+Z          Undo\n  Ctrl+Shift+Z / Ctrl+Y  Redo\n  Ctrl+Shift+E    Export\n  Ctrl+Shift+T    Edit tags\n  Ctrl+C / Ctrl+Y Copy text\n  Ctrl+L          Copy current line\n  Ctrl+F          Search preview\n  Ctrl+Shift+O    Search everywhere\n  Alt+G           Go to line\n  Ctrl+G          Select terminal text\n  Ctrl+R          Refresh\n  Ctrl+Shift+F    Focus mode\n") +
		"\nApp\n" + mutedSty.Render("  Ctrl+Q          Quit\n  ? / Esc         Close help\n") +
		"\n" + lipgloss.NewStyle().Foreground(accent).Render("Mouse and touch") + "\n" + mutedSty.Render("  Click rows and top actions. Scroll either panel.\n  In Select mode, drag over preview text; press any key to return.")
	box := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(1, 3).Width(min(70, max(32, m.width-4))).Render(help)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg))
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
