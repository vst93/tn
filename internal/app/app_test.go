package app

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/vnote/internal/storage"
)

func TestModelKeyboardAndMouseFlow(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, err := store.CreateDir("", "work")
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(dir, "today")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	if len(m.flat) != 2 {
		t.Fatalf("expected expanded folder and note, got %d rows", len(m.flat))
	}

	m.selectPath(note)
	m.openSelectedNote()
	if m.currentPath != note {
		t.Fatalf("expected note %q, got %q", note, m.currentPath)
	}
	m.toggleEdit()
	m.editor.SetValue("# Changed\n")
	if !m.dirty() {
		t.Fatal("expected edited note to be dirty")
	}
	if !m.save() {
		t.Fatal("save failed")
	}
	content, err := store.Read(note)
	if err != nil || content != "# Changed\n" {
		t.Fatalf("saved content = %q, %v", content, err)
	}

	m.active = treePane
	m.selectPath(dir)
	m.handleMouse(tea.MouseEvent{X: 3, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.expanded[dir] {
		t.Fatal("expected clicking directory row to collapse it")
	}
	if view := m.View(); view == "" {
		t.Fatal("view should not be empty")
	}
}

func TestCompactToolbarHitTargets(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(60, 24)
	if !m.compact {
		t.Fatal("expected compact layout")
	}
	if action := m.toolbarActionAt(1); action != "help" {
		t.Fatalf("first action = %q, want help", action)
	}
	if action := m.toolbarActionAt(11); action != "note" {
		t.Fatalf("second action = %q, want note", action)
	}
}

func TestCopyCurrentUsesMarkdownAndReportsResult(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "copy-me")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Unsaved selection\n")
	m.renderMarkdown()
	want := m.renderedPlain

	var copied string
	m.copier = func(content string) error {
		copied = content
		return nil
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected asynchronous copy command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if copied != want {
		t.Fatalf("copied %q, want rendered plain %q", copied, want)
	}
	if m.status != copyFeedback(copied) || m.statusErr || !m.statusOK {
		t.Fatalf("unexpected copy status %q, error=%v, ok=%v", m.status, m.statusErr, m.statusOK)
	}
}

func TestEditModeCopyCurrentLine(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "line-copy")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	m.editor.SetValue("line one\nline two")
	m.editor.CursorUp()
	m.editor.SetCursor(0)

	var copied string
	m.copier = func(content string) error {
		copied = content
		return nil
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected asynchronous copy command for Ctrl+L")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if copied != "line one" {
		t.Fatalf("copied %q, want current line", copied)
	}
}

func TestEditModeCopySelection(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "select-copy")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	m.editor.SetValue("abcdef")
	m.editor.SetCursor(0)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	m = updated.(Model)

	var copied string
	m.copier = func(content string) error {
		copied = content
		return nil
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected asynchronous copy command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if copied != "ab" {
		t.Fatalf("copied %q, want selection", copied)
	}
}

func TestCreateNoteAutoEntersEditMode(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)

	m.startPrompt(promptNote)
	m.input.SetValue("fresh")
	updated, _ := m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.mode != modeEdit {
		t.Fatalf("expected edit mode after creating a note, got mode %v", m.mode)
	}
	if m.active != contentPane {
		t.Fatalf("expected content pane active after creating a note, got %v", m.active)
	}
	if !m.editor.Focused() {
		t.Fatal("expected editor to be focused after creating a note")
	}
	if m.currentPath == "" {
		t.Fatal("expected newly created note to be opened")
	}
}

func TestStatusFlashAutoClears(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "status")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Changed\n")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a status timeout command after saving")
	}
	if m.status != "✓ Saved "+note || !m.statusOK {
		t.Fatalf("unexpected save status %q, ok=%v", m.status, m.statusOK)
	}

	updated, _ = m.Update(statusClearMsg{id: m.statusID})
	m = updated.(Model)
	if m.status != "" || m.statusOK {
		t.Fatalf("expected status to clear after timeout, got %q ok=%v", m.status, m.statusOK)
	}
}

func TestRenderMarkdownCachesRendererAndStylesCodeBlocks(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "preview")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Big Title\n\n```go\nfunc main() {}\n```\n")

	m.renderMarkdown()
	first := m.renderer
	if first == nil {
		t.Fatal("expected a cached renderer after first render")
	}
	content := m.preview.View()
	if !strings.Contains(content, "48;2;27;30;43") {
		t.Fatalf("expected code block background in preview, got: %q", content)
	}
	if stripped := stripANSI(content); strings.Contains(stripped, "CKSTART") || strings.Contains(stripped, "CKEND") {
		t.Fatalf("code block markers leaked into preview: %q", stripped)
	}

	m.renderMarkdown()
	if m.renderer != first {
		t.Fatal("expected renderer to be reused when width is unchanged")
	}

	m.resize(70, 30)
	if m.renderer == first {
		t.Fatal("expected renderer to be rebuilt when width changes")
	}

	title := regexp.MustCompile(`(?m)^\s*BIG TITLE`).FindString(stripANSI(m.preview.View()))
	if title == "" {
		t.Fatalf("expected uppercased H1 title in preview: %q", stripANSI(m.preview.View()))
	}
}

func TestFindSearchMatchesIsCaseInsensitive(t *testing.T) {
	matches := findSearchMatches("Hello World\nhello again", "HELLO")
	if len(matches) != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d", len(matches))
	}
	if matches[0].line != 0 || matches[0].start != 0 || matches[0].end != 5 {
		t.Fatalf("unexpected first match %+v", matches[0])
	}
	if matches[1].line != 1 || matches[1].start != 0 {
		t.Fatalf("unexpected second match %+v", matches[1])
	}
}

func TestHighlightSearchContentUsesReverse(t *testing.T) {
	content := "Hello World\nhello again"
	out := highlightSearchContent(content, "hello")
	if !strings.Contains(out, "\x1b[7m") {
		t.Fatalf("expected reverse highlight codes, got %q", out)
	}
	if strings.Count(out, "\x1b[7m") != 2 {
		t.Fatalf("expected both matches highlighted, got %q", out)
	}
}

func TestSearchModeNavigatesAndExits(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "search-note")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("alpha beta alpha\nbeta alpha")
	m.renderMarkdown()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = updated.(Model)
	if m.mode != modeSearch {
		t.Fatalf("expected search mode after Ctrl+F, got %v", m.mode)
	}
	if !m.input.Focused() {
		t.Fatal("expected search input to be focused")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha")})
	m = updated.(Model)
	want := findSearchMatches(m.renderedPlain, "alpha")
	if len(m.searchMatches) != len(want) || len(m.searchMatches) == 0 {
		t.Fatalf("expected %d search matches, got %d", len(want), len(m.searchMatches))
	}
	if !strings.Contains(m.preview.View(), "\x1b[7m") {
		t.Fatal("expected highlight codes in preview while searching")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.searchIndex != 1 {
		t.Fatalf("expected Enter to advance to match 2, got index %d", m.searchIndex)
	}
	if m.input.Focused() {
		t.Fatal("expected search input to blur after Enter")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.searchIndex != 2 {
		t.Fatalf("expected n to advance to match 3, got index %d", m.searchIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m = updated.(Model)
	if m.searchIndex != 1 {
		t.Fatalf("expected N to go back to match 2, got index %d", m.searchIndex)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected Esc to leave search mode, got %v", m.mode)
	}
	if len(m.searchMatches) != 0 {
		t.Fatal("expected search matches cleared on exit")
	}
	if strings.Contains(m.preview.View(), "\x1b[7m") {
		t.Fatal("expected highlights cleared after leaving search mode")
	}
}

func TestGotoLineInEditMode(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "goto-note")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	m.editor.SetValue("line one\nline two\nline three\nline four")

	m.startGotoLine()
	m.input.SetValue("3")
	updated, _ := m.updateGotoLinePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected to return to edit mode after goto line, got %v", m.mode)
	}
	if m.editor.Line() != 2 {
		t.Fatalf("expected cursor on line 3 (0-based 2), got %d", m.editor.Line())
	}
}

func TestGotoLineClampsToLastLineInPreview(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "goto-preview")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString("line of content number " + fmt.Sprint(i) + "\n")
	}
	m.editor.SetValue(b.String())
	m.renderMarkdown()

	m.startGotoLine()
	m.input.SetValue("999")
	updated, _ := m.updateGotoLinePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected preview mode after goto line, got %v", m.mode)
	}
	if m.preview.YOffset <= 0 {
		t.Fatalf("expected out-of-range line to scroll to last, got offset %d", m.preview.YOffset)
	}
	if !m.statusErr {
		t.Fatal("expected out-of-range status message")
	}
}

func TestEditStatusBarShowsPosition(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "status-edit")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	m.editor.SetValue("a\nb\nc")
	m.gotoLineEdit(0)

	bar := stripANSI(m.editShortcutBar())
	if !strings.Contains(bar, "Ln 1 / 3 · Col 1") {
		t.Fatalf("edit status bar missing position: %q", bar)
	}
}

func TestPreviewStatusBarShowsStats(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "status-preview")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("one two three\nfour five")
	m.renderMarkdown()

	bar := stripANSI(m.shortcutBar())
	if !strings.Contains(bar, "5 words · 2 lines · ~1 min read") {
		t.Fatalf("preview status bar missing stats: %q", bar)
	}
}

func TestAutoIndentOnEnter(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "indent-note")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	m.editor.SetValue("- item")
	m.editor.SetCursor(6)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editor.Value() != "- item\n- " {
		t.Fatalf("expected list continuation, got %q", m.editor.Value())
	}
}

func TestAutoIndentInheritsIndentation(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "indent-space")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	m.editor.SetValue("  hello")
	m.editor.SetCursor(7)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editor.Value() != "  hello\n  " {
		t.Fatalf("expected indentation inherited, got %q", m.editor.Value())
	}
}

func TestSelectionModeDisablesThenRestoresMouse(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected selection mode command")
	}
	// Sequence first disables mouse, then emits selectionModeMsg.
	msg := cmd()
	if msg == nil {
		t.Fatal("expected sequence message")
	}
	m.selecting = true
	updated, restore := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.selecting || restore == nil {
		t.Fatal("expected selection mode to restore mouse on key press")
	}
}
