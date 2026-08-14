package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
)

type promptKind int

const (
	promptNote promptKind = iota
	promptDir
	promptRename
)

type flatNode struct {
	node  *storage.Node
	depth int
}

type toolbarItem struct{ label, action string }

type copyResultMsg struct{ err error }
type selectionModeMsg struct{}

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

	currentPath string
	original    string
	editor      textarea.Model
	preview     viewport.Model
	input       textinput.Model
	promptKind  promptKind

	width, height int
	treeWidth     int
	bodyHeight    int
	compact       bool
	selecting     bool
	copier        func(string) error
	pending       tea.Cmd

	renderer      *glamour.TermRenderer
	rendererWidth int

	status    string
	statusErr bool
	confirm   string
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
		store:    store,
		expanded: make(map[string]bool),
		active:   treePane,
		mode:     modeNormal,
		editor:   editor,
		preview:  viewport.New(60, 20),
		input:    input,
		copier:   copyText,
		status:   "Ready",
	}
	m.preview.MouseWheelEnabled = true
	m.preview.MouseWheelDelta = 3
	m.refresh("")
	return m
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
		if m.mode == modeNormal || m.mode == modeEdit {
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

		if handled, quit := m.globalKey(key); handled {
			if quit {
				return m, tea.Quit
			}
			return m, m.takePending()
		}

		if m.mode == modeEdit {
			m.editor, cmd = m.editor.Update(msg)
			return m, cmd
		}
		if m.active == treePane {
			m.treeKey(key)
			return m, nil
		}
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	case copyResultMsg:
		if msg.err != nil {
			m.setStatus("Copy failed: "+msg.err.Error(), true)
		} else {
			m.setStatus("Copied Markdown to clipboard", false)
		}
		return m, nil
	case selectionModeMsg:
		m.selecting = true
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
		return true, true
	case "ctrl+s":
		m.save()
		return true, false
	case "ctrl+n":
		m.startPrompt(promptNote)
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
		m.startPrompt(promptNote)
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
		return true, true
	case "ctrl+r":
		m.refresh(m.selectedPath())
		m.setStatus("Notebook refreshed", false)
		return true, false
	case "ctrl+y":
		m.copyCurrent()
		return true, false
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
		if m.mode == modeEdit {
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
			m.active = treePane
		} else if msg.X >= 20 && msg.X < 34 {
			m.active = contentPane
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
		m.active = treePane
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

	m.active = contentPane
	if msg.Button == tea.MouseButtonLeft && m.currentPath != "" && m.mode == modeEdit {
		m.editor.Focus()
	}
	if m.mode == modeNormal && msg.IsWheel() {
		updated, _ := m.preview.Update(tea.MouseMsg(msg))
		m.preview = updated
	}
}

func (m *Model) runAction(action string) {
	switch action {
	case "note":
		m.startPrompt(promptNote)
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
		m.setStatus("Open a note first", true)
		return
	}
	content := m.editor.Value()
	copier := m.copier
	if copier == nil {
		copier = copyText
	}
	m.setStatus("Copying…", false)
	m.pending = copyCmd(copier, content)
}

func (m *Model) takePending() tea.Cmd {
	cmd := m.pending
	m.pending = nil
	return cmd
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
	return func() tea.Msg { return copyResultMsg{err: copier(content)} }
}

func (m *Model) togglePane() {
	if m.active == treePane {
		m.active = contentPane
	} else {
		m.active = treePane
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
		m.active = contentPane
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
	m.preview.GotoTop()
	m.renderMarkdown()
	m.setStatus("Opened "+path, false)
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
		m.setStatus("Select an item to rename", true)
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
		} else if m.promptKind == promptDir {
			m.setStatus("Created "+path, false)
		} else {
			m.setStatus("Renamed to "+path, false)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
		m.setStatus("Nothing selected", true)
		return
	}
	n := m.flat[m.selected].node
	if n.IsDir {
		m.confirm = "Delete folder “" + n.Name + "” and all notes inside?"
	} else {
		m.confirm = "Delete note “" + n.Name + "”?"
	}
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
			m.preview.SetContent("")
		}
		if err := m.store.Delete(path); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.refresh("")
			m.setStatus("Deleted "+path, false)
		}
		m.mode = modeNormal
	case "n", "N", "esc":
		m.mode = modeNormal
		m.setStatus("Delete cancelled", false)
	}
	return m, nil
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
	m.editor.Focus()
	m.setStatus("Editing · Ctrl+S to save · Esc to preview", false)
}

func (m *Model) leaveEdit() {
	m.editor.Blur()
	m.mode = modeNormal
	m.renderMarkdown()
	if m.dirty() {
		m.setStatus("Unsaved changes · Ctrl+S to save", false)
	} else {
		m.setStatus("Preview mode", false)
	}
}

func (m *Model) save() bool {
	if m.currentPath == "" || !m.dirty() {
		m.setStatus("Nothing to save", false)
		return true
	}
	content := m.editor.Value()
	if err := m.store.Write(m.currentPath, content); err != nil {
		m.setStatus(err.Error(), true)
		return false
	}
	m.original = content
	m.renderMarkdown()
	m.setStatus("Saved "+m.currentPath+" at "+time.Now().Format("15:04"), false)
	return true
}

func (m *Model) dirty() bool {
	return m.currentPath != "" && m.editor.Value() != m.original
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
	var walk func([]*storage.Node, int)
	walk = func(nodes []*storage.Node, depth int) {
		for _, n := range nodes {
			m.flat = append(m.flat, flatNode{node: n, depth: depth})
			if n.IsDir && m.expanded[n.RelPath] {
				walk(n.Children, depth+1)
			}
		}
	}
	walk(m.tree, 0)
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
	m.preview.Height = max(1, m.contentHeight()-2)
	m.ensureSelectionVisible()
	m.renderMarkdown()
}

func (m *Model) renderMarkdown() {
	if m.currentPath == "" {
		m.preview.SetContent("")
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
			return
		}
		m.renderer = renderer
		m.rendererWidth = width
	}
	rendered, err := m.renderer.Render(m.editor.Value())
	if err != nil {
		m.setStatus("Markdown preview: "+err.Error(), true)
		m.preview.SetContent(m.editor.Value())
		return
	}
	m.preview.SetContent(strings.TrimSpace(decorateCodeBlocks(rendered, width)))
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
			Text:              glamansi.StylePrimitive{Color: stringPtr(textColor)},
			Background:        glamansi.StylePrimitive{BackgroundColor: stringPtr(surfaceColor)},
			Comment:           glamansi.StylePrimitive{Color: stringPtr(mutedColor), Italic: boolPtr(true)},
			CommentPreproc:    glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Keyword:           glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			KeywordType:       glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			Name:              glamansi.StylePrimitive{Color: stringPtr(textColor)},
			NameBuiltin:       glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			NameTag:           glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			NameAttribute:     glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			NameClass:         glamansi.StylePrimitive{Color: stringPtr(textColor), Bold: boolPtr(true)},
			NameConstant:      glamansi.StylePrimitive{Color: stringPtr(textColor)},
			NameFunction:      glamansi.StylePrimitive{Color: stringPtr(accentColor)},
			Literal:           glamansi.StylePrimitive{Color: stringPtr(textColor)},
			LiteralNumber:     glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			LiteralString:     glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			LiteralStringEscape: glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Operator:          glamansi.StylePrimitive{Color: stringPtr(warningColor)},
			Punctuation:       glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
			GenericDeleted:    glamansi.StylePrimitive{Color: stringPtr(dangerColor)},
			GenericInserted:   glamansi.StylePrimitive{Color: stringPtr(greenColor)},
			GenericEmph:       glamansi.StylePrimitive{Italic: boolPtr(true)},
			GenericStrong:     glamansi.StylePrimitive{Bold: boolPtr(true)},
			GenericSubheading: glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
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
	m.status, m.statusErr = message, isError
}

func (m Model) View() string {
	if m.width <= 1 || m.height <= 1 {
		return "Vnote"
	}
	if m.mode == modeHelp {
		return m.helpView()
	}
	if m.mode == modePrompt || m.mode == modeConfirm {
		return m.dialogView()
	}

	header := m.headerView()
	var body string
	if m.compact {
		if m.active == treePane {
			body = m.treeView(m.width)
		} else {
			body = m.contentView(m.width)
		}
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.treeView(m.treeWidth), m.contentView(m.width-m.treeWidth))
	}
	status := m.statusView()
	view := header + "\n" + body + "\n" + status
	return lipgloss.NewStyle().Background(bg).Width(m.width).Height(m.height).Render(view)
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
	modeName := "preview"
	if m.mode == modeEdit {
		modeName = "edit"
	}
	if m.selecting {
		modeName = "select"
	}
	right := mutedSty.Render(modeName+"  ·  ") + truncateANSI(name, max(1, m.width-lipgloss.Width(left)-12))
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
		{"? help", "help"}, {"n note", "note"}, {"d folder", "folder"}, {"e edit", "edit"}, {"s save", "save"}, {"y copy", "copy"}, {"g select", "select"}, {"r rename", "rename"}, {"x delete", "delete"}, {"q quit", "quit"},
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
	focused := m.active == treePane
	title := "Lists"
	if len(m.flat) > 0 {
		title += mutedSty.Render("  " + fmt.Sprintf("%d", len(m.flat)))
	}
	var lines []string
	rows := m.treeRows()
	end := min(len(m.flat), m.treeOffset+rows)
	for i := m.treeOffset; i < end; i++ {
		item := m.flat[i]
		marker := "  "
		if item.node.IsDir {
			if m.expanded[item.node.RelPath] {
				marker = "▾ "
			} else {
				marker = "▸ "
			}
		} else {
			marker = "○ "
		}
		name := item.node.Name
		if !item.node.IsDir {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		label := strings.Repeat("  ", item.depth) + marker + name
		label = truncate(label, max(1, width-5))
		line := " " + label
		if i == m.selected {
			fg := text
			if focused {
				fg = accent
			}
			line = lipgloss.NewStyle().Background(selection).Foreground(fg).Bold(focused).Width(max(1, width-2)).Render(line)
		} else if item.node.IsDir {
			line = lipgloss.NewStyle().Foreground(text).Render(line)
		} else {
			line = mutedSty.Render(line)
		}
		lines = append(lines, line)
	}
	if len(m.flat) == 0 {
		lines = append(lines, mutedSty.Render("  No notes"), mutedSty.Render("  Press n to create one"))
	}
	return borderedPanel(title, strings.Join(lines, "\n"), width, m.bodyHeight, focused)
}

func (m Model) contentView(width int) string {
	focused := m.active == contentPane
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
		meta = mutedSty.Render("  "+m.currentPath) + "   " +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render(state) + "   " +
			mutedSty.Render(modeName) + "   " +
			mutedSty.Render(fmt.Sprintf("%d words", words))
		if m.mode != modeEdit {
			meta = " " + meta + "\n"
		} else {
			meta = " " + meta + "\n"
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

	return borderedPanel(title, meta+content, width, m.bodyHeight, focused)
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
	width, height = max(4, width), max(3, height)
	borderColor := rule
	titleColor := muted
	if focused {
		borderColor = accent
		titleColor = accent
	}
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	title = truncate(title, max(1, innerWidth-2))
	titleText := lipgloss.NewStyle().Foreground(titleColor).Bold(focused).Render(" " + title + " ")
	topTail := max(0, innerWidth-lipgloss.Width(" "+title+" "))
	top := lipgloss.NewStyle().Foreground(borderColor).Render("┌") + titleText + lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("─", topTail)+"┐")

	content = fitBlock(content, innerWidth, innerHeight)
	rows := strings.Split(content, "\n")
	for i, row := range rows {
		rows[i] = lipgloss.NewStyle().Foreground(borderColor).Render("│") + truncateANSI(row, innerWidth) + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(row))) + lipgloss.NewStyle().Foreground(borderColor).Render("│")
	}
	bottom := lipgloss.NewStyle().Foreground(borderColor).Render("└" + strings.Repeat("─", innerWidth) + "┘")
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
	if m.statusErr {
		message := errorSty.Render("! " + truncate(m.status, max(1, m.width-3)))
		return lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render(" " + message)
	}
	if m.selecting {
		return lipgloss.NewStyle().Foreground(accent).Width(m.width).MaxHeight(1).Render(" Select text now · terminal may auto-copy · press any key to return")
	}
	return m.shortcutBar()
}

func (m Model) shortcutBar() string {
	var b strings.Builder
	b.WriteString(" ")
	for _, item := range m.toolbarItems() {
		key, label, _ := strings.Cut(item.label, " ")
		chunk := "[" + key + "] " + label + "  "
		if lipgloss.Width(b.String())+lipgloss.Width(chunk) > m.width {
			break
		}
		b.WriteString(lipgloss.NewStyle().Foreground(accent).Render("[" + key + "]"))
		b.WriteString(" " + lipgloss.NewStyle().Foreground(muted).Render(label) + "  ")
	}
	status := truncate(m.status, max(0, m.width-lipgloss.Width(b.String())-2))
	if status != "" && m.width-lipgloss.Width(b.String()) > lipgloss.Width(status)+1 {
		b.WriteString(strings.Repeat(" ", m.width-lipgloss.Width(b.String())-lipgloss.Width(status)-1))
		b.WriteString(statusSty.Render(status))
	}
	return lipgloss.NewStyle().Foreground(muted).Width(m.width).MaxHeight(1).Render(b.String())
}

func (m Model) dialogView() string {
	var title, body string
	if m.mode == modeConfirm {
		title = "Delete"
		body = m.confirm + "\n\n" + dangerStyle("Enter / Y  delete") + "  " + mutedSty.Render("Esc / N  cancel")
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
	dialog := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.NormalBorder()).BorderForeground(rule).Padding(1, 2).Width(min(64, max(28, m.width-6))).Render(brandSty.Render(title) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func (m Model) helpView() string {
	help := brandSty.Render("Vnote shortcuts") + "\n\n" +
		"Navigate\n" + mutedSty.Render("  ↑/↓ or J/K     Select item\n  ←/→ or H/L     Collapse / expand\n  Enter           Open note\n  Tab             Switch panel\n") +
		"\nNotes\n" + mutedSty.Render("  Ctrl+N          New note\n  Ctrl+D          New folder\n  F2              Rename\n  Delete          Delete\n  Ctrl+E          Edit / preview\n  Ctrl+S          Save\n  Ctrl+C / Ctrl+Y Copy Markdown\n  Ctrl+G          Select terminal text\n  Ctrl+R          Refresh\n") +
		"\nApp\n" + mutedSty.Render("  Ctrl+Q          Quit\n  ? / Esc         Close help\n") +
		"\n" + lipgloss.NewStyle().Foreground(accent).Render("Mouse and touch") + "\n" + mutedSty.Render("  Click rows and top actions. Scroll either panel.\n  In Select mode, drag over preview text; press any key to return.")
	box := lipgloss.NewStyle().Background(surface).Foreground(text).Border(lipgloss.NormalBorder()).BorderForeground(rule).Padding(1, 3).Width(min(70, max(32, m.width-4))).Render(help)
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
