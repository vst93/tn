package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
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

func TestSplitViewSeparatorVisible(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "sep")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	if m.compact {
		t.Fatal("expected split layout at width 100")
	}
	m.selectPath(note)
	m.openSelectedNote()
	lines := strings.Split(stripANSI(m.View()), "\n")
	for i := 1; i <= m.bodyHeight; i++ {
		runes := []rune(lines[i])
		if m.treeWidth >= len(runes) || string(runes[m.treeWidth]) != "│" {
			t.Fatalf("line %d missing separator at col %d: %q", i, m.treeWidth, lines[i])
		}
	}
}

func TestNoteAndFolderShortcuts(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	if handled, _ := m.globalKey("n"); !handled {
		t.Fatal("expected n to be handled")
	}
	if m.mode != modePrompt || m.promptKind != promptNote {
		t.Fatalf("expected note prompt from n, mode=%v kind=%v", m.mode, m.promptKind)
	}

	m.mode = modeNormal
	if handled, _ := m.globalKey("N"); !handled {
		t.Fatal("expected N to be handled")
	}
	if m.mode != modePrompt || m.promptKind != promptDir {
		t.Fatalf("expected folder prompt from N, mode=%v kind=%v", m.mode, m.promptKind)
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
	if action := m.toolbarActionAt(11); action != "tagfilter" {
		t.Fatalf("second action = %q, want tagfilter", action)
	}
	if action := m.toolbarActionAt(21); action != "note" {
		t.Fatalf("third action = %q, want note", action)
	}
}

func TestHeaderTabClickTargets(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	// "◆ tn │  Lists  │  Preview "
	//    1234567890123456789012345678
	if p, ok := m.headerTabAt(13); !ok || p != treePane {
		t.Fatalf("expected Lists tab at x=13, got pane=%v ok=%v", p, ok)
	}
	if p, ok := m.headerTabAt(23); !ok || p != contentPane {
		t.Fatalf("expected Preview tab at x=23, got pane=%v ok=%v", p, ok)
	}
	if _, ok := m.headerTabAt(0); ok {
		t.Fatal("expected no tab at x=0")
	}
	if _, ok := m.headerTabAt(60); ok {
		t.Fatal("expected no tab at x=60")
	}

	m.mode = modeEdit
	// Edit tab is shorter: " Edit " at positions 18-23
	if p, ok := m.headerTabAt(21); !ok || p != contentPane {
		t.Fatalf("expected Edit tab at x=21, got pane=%v ok=%v", p, ok)
	}
	if _, ok := m.headerTabAt(24); ok {
		t.Fatal("expected x=24 outside the Edit tab in edit mode")
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

func TestSaveKeyInPreviewAndEditModes(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "save-keys")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()

	// Preview mode: plain 's' saves the note.
	m.editor.SetValue("# Preview edit\n")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected preview mode, got %v", m.mode)
	}
	if m.status != "✓ Saved "+note || !m.statusOK {
		t.Fatalf("expected 's' to save in preview mode, got status %q ok=%v", m.status, m.statusOK)
	}
	if content, err := store.Read(note); err != nil || content != "# Preview edit\n" {
		t.Fatalf("preview-mode save wrote %q, %v", content, err)
	}

	// Edit mode: plain 's' must type into the editor, not save.
	m.toggleEdit()
	m.editor.SetValue("abc")
	m.editor.SetCursor(3)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected edit mode, got %v", m.mode)
	}
	if m.editor.Value() != "abcs" {
		t.Fatalf("expected 's' to be typed in edit mode, got %q", m.editor.Value())
	}
	if content, _ := store.Read(note); content != "# Preview edit\n" {
		t.Fatalf("edit-mode 's' must not save, store content = %q", content)
	}

	// Edit mode: Ctrl+S saves.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if m.status != "✓ Saved "+note || !m.statusOK {
		t.Fatalf("expected Ctrl+S to save in edit mode, got status %q ok=%v", m.status, m.statusOK)
	}
	if content, err := store.Read(note); err != nil || content != "abcs" {
		t.Fatalf("edit-mode Ctrl+S wrote %q, %v", content, err)
	}
}

func TestNoNoteStatusBarShowsHint(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)

	// After a transient status expires, the bar must still show a useful hint.
	updated, _ := m.Update(statusClearMsg{id: m.statusID})
	m = updated.(Model)
	if m.status != "" {
		t.Fatalf("expected status to clear, got %q", m.status)
	}

	bar := stripANSI(m.shortcutBar())
	if !strings.Contains(bar, "TN") {
		t.Fatalf("no-note status bar missing app name: %q", bar)
	}
	if !strings.Contains(bar, "select a note") {
		t.Fatalf("no-note status bar missing hint: %q", bar)
	}
	if !strings.Contains(bar, "[n] note") {
		t.Fatalf("no-note status bar missing toolbar shortcuts: %q", bar)
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
	m.editor.SetCursor(0)

	bar := stripANSI(m.editShortcutBar())
	if !strings.Contains(bar, "Line 1/3 · Col 1") {
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
	if !strings.Contains(bar, "[?] help") {
		t.Fatalf("preview status bar missing shortcuts: %q", bar)
	}
	m.setStatus("Status right", false)
	bar = stripANSI(m.shortcutBar())
	if !strings.Contains(bar, "Status right") {
		t.Fatalf("preview status bar missing right-side status: %q", bar)
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

func openEditModel(t *testing.T, name string) (Model, *storage.Store, string) {
	t.Helper()
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", name)
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	return m, store, note
}

func TestUndoRedoInEditMode(t *testing.T) {
	m, _, _ := openEditModel(t, "undo-note")
	m.editor.SetValue("hello")
	m.editor.SetCursor(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = updated.(Model)
	if m.editor.Value() != "hello!" {
		t.Fatalf("expected hello!, got %q", m.editor.Value())
	}
	if !m.undoable() {
		t.Fatal("expected undo to be available after an edit")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	m = updated.(Model)
	if m.editor.Value() != "hello" {
		t.Fatalf("expected undo to restore hello, got %q", m.editor.Value())
	}
	if !m.redoable() {
		t.Fatal("expected redo to be available after undo")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	m = updated.(Model)
	if m.editor.Value() != "hello!" {
		t.Fatalf("expected Ctrl+Y to redo, got %q", m.editor.Value())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	m = updated.(Model)
	if handled, _ := m.globalKey("ctrl+shift+z"); !handled {
		t.Fatal("expected Ctrl+Shift+Z to be handled in edit mode")
	}
	if m.editor.Value() != "hello!" {
		t.Fatalf("expected Ctrl+Shift+Z to redo, got %q", m.editor.Value())
	}
}

func TestSaveClearsRedoStack(t *testing.T) {
	m, store, note := openEditModel(t, "save-redo")
	m.editor.SetValue("abc")
	m.editor.SetCursor(3)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	m = updated.(Model)
	if m.editor.Value() != "abc" || !m.redoable() {
		t.Fatalf("expected undo to abc with redo available, value=%q redo=%v", m.editor.Value(), m.redoable())
	}

	if !m.save() {
		t.Fatal("save failed")
	}
	if m.redoable() || len(m.redoStack) != 0 {
		t.Fatalf("expected redo stack cleared after save, redo=%v len=%d", m.redoable(), len(m.redoStack))
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	m = updated.(Model)
	if m.editor.Value() != "abc" {
		t.Fatalf("expected redo to be a no-op after save, got %q", m.editor.Value())
	}
	if content, err := store.Read(note); err != nil || content != "abc" {
		t.Fatalf("saved content = %q, %v", content, err)
	}
}

func TestEditStatusBarShowsUndoRedo(t *testing.T) {
	m, _, _ := openEditModel(t, "bar-undo")
	m.editor.SetValue("a")
	m.editor.SetCursor(1)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(Model)

	bar := stripANSI(m.editShortcutBar())
	if !strings.Contains(bar, "Ctrl+Z") || !strings.Contains(bar, "Ctrl+Shift+Z") {
		t.Fatalf("edit status bar missing undo/redo hints: %q", bar)
	}
	if !m.undoable() {
		t.Fatal("expected undo hint to be active after an edit")
	}
}

func TestExportShortcutTriggersDialog(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "export-trigger")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()

	if handled, _ := m.globalKey("ctrl+shift+e"); !handled {
		t.Fatal("expected Ctrl+Shift+E to be handled in preview mode")
	}
	if m.mode != modeExport {
		t.Fatalf("expected export mode, got %v", m.mode)
	}
	if view := m.View(); view == "" {
		t.Fatal("expected export dialog view to render")
	}
}

func TestExportShortcutDisabledInEditMode(t *testing.T) {
	m, _, _ := openEditModel(t, "export-edit")
	if handled, _ := m.globalKey("ctrl+shift+e"); handled {
		t.Fatal("expected Ctrl+Shift+E not to be handled in edit mode")
	}
	if m.mode != modeEdit {
		t.Fatalf("expected to stay in edit mode, got %v", m.mode)
	}
}

func TestExportCopyToClipboard(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "export-copy")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Export me\n")
	m.renderMarkdown()
	want := m.renderedPlain

	m.startExport()
	if m.mode != modeExport || m.exportPath {
		t.Fatalf("expected export menu, mode=%v path=%v", m.mode, m.exportPath)
	}
	var copied string
	m.copier = func(content string) error {
		copied = content
		return nil
	}
	updated, cmd := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected copy command from export")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if copied != want {
		t.Fatalf("copied %q, want rendered plain %q", copied, want)
	}
	if m.status != "✓ Copied to clipboard" || !m.statusOK {
		t.Fatalf("unexpected copy status %q, ok=%v", m.status, m.statusOK)
	}
}

func TestExportSaveAs(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "export-save")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Exported\n")

	m.startExport()
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	if !m.exportPath {
		t.Fatal("expected save-as path input after choosing option 2")
	}
	defaultPath := filepath.Join(store.Root, "export-save.md")
	if m.input.Value() != defaultPath {
		t.Fatalf("default export path = %q, want %q", m.input.Value(), defaultPath)
	}

	m.input.SetValue("copy.md")
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	wantPath := filepath.Join(store.Root, "copy.md")
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Exported\n" {
		t.Fatalf("exported content = %q", string(data))
	}
	if m.status != "✓ Exported to "+wantPath || !m.statusOK {
		t.Fatalf("unexpected export status %q, ok=%v", m.status, m.statusOK)
	}
}

func TestExportInvalidPathStaysInDialog(t *testing.T) {
	m, _, _ := openEditModel(t, "export-bad")
	m.startExport()
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	m.input.SetValue("")
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeExport || !m.exportPath {
		t.Fatalf("expected to stay in export path input, mode=%v path=%v", m.mode, m.exportPath)
	}
	if !m.statusErr {
		t.Fatal("expected error status for empty export path")
	}
}

func TestExportEscCancels(t *testing.T) {
	m, _, _ := openEditModel(t, "export-esc")
	m.startExport()
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected Esc to cancel export, mode=%v", m.mode)
	}
}

func TestFocusModeTogglesAndRendersFullScreen(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	if handled, _ := m.globalKey("ctrl+shift+f"); !handled {
		t.Fatal("expected Ctrl+Shift+F to be handled")
	}
	if !m.focusing {
		t.Fatal("expected focus mode to be enabled")
	}
	view := m.View()
	if !strings.Contains(view, "Esc 退出专注") {
		t.Fatalf("expected focus hint in view, got %q", view)
	}
	if strings.Contains(view, "◆ tn") {
		t.Fatal("expected header to be hidden in focus mode")
	}

	if handled, _ := m.globalKey("esc"); !handled {
		t.Fatal("expected Esc to be handled in focus mode")
	}
	if m.focusing {
		t.Fatal("expected Esc to exit focus mode")
	}
}

func TestFocusModeKeepsEditControls(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "focus-edit")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	before := m.editor.Width()

	if handled, _ := m.globalKey("ctrl+shift+f"); !handled {
		t.Fatal("expected Ctrl+Shift+F to be handled in edit mode")
	}
	if !m.focusing || m.mode != modeEdit || !m.editor.Focused() {
		t.Fatalf("expected focused edit mode, focusing=%v mode=%v focused=%v", m.focusing, m.mode, m.editor.Focused())
	}
	if m.editor.Width() <= before {
		t.Fatalf("expected editor to widen in focus mode, before=%d after=%d", before, m.editor.Width())
	}

	m.editor.SetValue("focused content")
	if !m.dirty() {
		t.Fatal("expected editing to remain available in focus mode")
	}
	if handled, _ := m.globalKey("ctrl+s"); !handled {
		t.Fatal("expected Ctrl+S to save in focus mode")
	}
}

func TestSessionPersistsAndRestores(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, err := store.CreateDir("", "work")
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(dir, "session")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.sessionPath = filepath.Join(t.TempDir(), "session.json")
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n\n", i))
	}
	m.editor.SetValue(sb.String())
	if err := store.Write(note, m.editor.Value()); err != nil {
		t.Fatal(err)
	}
	m.renderMarkdown()
	m.expanded[dir] = false
	m.treeOffset = 1
	m.preview.SetYOffset(3)
	m.toggleEdit()
	m.gotoLineEdit(4)
	m.editor.SetCursor(2)
	m.saveSession()

	if _, err := os.Stat(m.sessionPath); err != nil {
		t.Fatalf("expected session file to be written: %v", err)
	}

	restored := New(store)
	restored.sessionPath = m.sessionPath
	restored = restored.restoreSession()
	if restored.currentPath != note {
		t.Fatalf("expected restored path %q, got %q", note, restored.currentPath)
	}
	if restored.mode != modeEdit || restored.active != contentPane {
		t.Fatalf("expected restored edit mode, mode=%v active=%v", restored.mode, restored.active)
	}
	pos := restored.cursorPos()
	if pos.row != 4 || pos.col != 2 {
		t.Fatalf("expected restored cursor row=5 col=3, got %+v", pos)
	}
	if restored.expanded[dir] {
		t.Fatal("expected restored collapsed folder state")
	}
	if restored.preview.YOffset != 3 {
		t.Fatalf("expected restored preview offset 3, got %d", restored.preview.YOffset)
	}
}

func TestRestoreSessionHandlesCorruptedFile(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "survivor")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		data string
	}{
		{"garbage", "{not valid json!!"},
		{"truncated", `{"currentPath": "`},
		{"wrong types", `{"currentPath": 42, "cursorRow": "x", "expanded": "nope"}`},
		{"hostile numbers", `{"cursorRow": -5, "cursorCol": 999999, "treeOffset": -3, "previewOff": -1}`},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			m := New(store)
			m.sessionPath = path
			restored := m.restoreSession()
			if restored.currentPath != "" {
				t.Fatalf("expected no note restored from corrupt session, got %q", restored.currentPath)
			}
			if restored.editor.Value() != "" {
				t.Fatalf("expected empty editor, got %q", restored.editor.Value())
			}
		})
	}

	// A valid session must still restore normally, i.e. a corrupt file
	// encountered earlier must not poison later restores.
	good := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(good, []byte(`{"currentPath": "`+note+`", "mode": "preview"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.sessionPath = good
	restored := m.restoreSession()
	if restored.currentPath != note {
		t.Fatalf("expected valid session to restore %q, got %q", note, restored.currentPath)
	}
}

func TestGlobalSearchFindsContentAndOpensAtLine(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "topic")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(fmt.Sprintf("filler %d\n", i))
	}
	b.WriteString("needle line")
	if err := store.Write(note, b.String()); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	if handled, _ := m.globalKey("ctrl+shift+o"); !handled {
		t.Fatal("expected Ctrl+Shift+O to be handled")
	}
	if m.mode != modeSearchGlobal {
		t.Fatalf("expected global search mode, got %v", m.mode)
	}
	if view := m.View(); view == "" {
		t.Fatal("expected global search view to render")
	}

	m.input.SetValue("needle")
	updated, cmd := m.updateGlobalSearch(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected debounced search command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if len(m.globalSearchResults) != 1 {
		t.Fatalf("expected 1 result, got %d", len(m.globalSearchResults))
	}
	if m.globalSearchResults[0].lineNum != 21 || m.globalSearchResults[0].snippet != "needle line" {
		t.Fatalf("unexpected result %+v", m.globalSearchResults[0])
	}

	m.openGlobalSearchResult()
	if m.currentPath != note {
		t.Fatalf("expected opened note %q, got %q", note, m.currentPath)
	}
	if m.mode != modeEdit {
		t.Fatalf("expected to stay in edit mode, got %v", m.mode)
	}
	if m.editor.Line() != 20 {
		t.Fatalf("expected cursor on line 21 (0-based 20), got %d", m.editor.Line())
	}
}

func TestGlobalSearchMatchesTitlesAndNavigates(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "quarterly-report")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "other-notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, "# Report\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, "quarterly totals here"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.runGlobalSearch("quarterly")
	if len(m.globalSearchResults) != 2 {
		t.Fatalf("expected 2 results, got %d", len(m.globalSearchResults))
	}
	titles := map[string]bool{}
	for _, r := range m.globalSearchResults {
		titles[r.title] = true
	}
	if !titles["quarterly-report"] || !titles["other-notes"] {
		t.Fatalf("unexpected results %+v", m.globalSearchResults)
	}

	m.moveGlobalSearch(1)
	if m.globalSearchIndex != 1 {
		t.Fatalf("expected index 1 after down, got %d", m.globalSearchIndex)
	}
	m.moveGlobalSearch(1)
	if m.globalSearchIndex != 0 {
		t.Fatalf("expected wrap to 0, got %d", m.globalSearchIndex)
	}

	m.globalSearchIndex = -1
	for i, r := range m.globalSearchResults {
		if r.path == noteA {
			m.globalSearchIndex = i
		}
	}
	m.openGlobalSearchResult()
	if m.currentPath != noteA {
		t.Fatalf("expected opened note %q, got %q", noteA, m.currentPath)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected preview mode after open, got %v", m.mode)
	}
}

func TestGlobalSearchEscRestoresMode(t *testing.T) {
	m, _, _ := openEditModel(t, "gs-esc")
	m.globalKey("ctrl+shift+o")
	if m.mode != modeSearchGlobal {
		t.Fatalf("expected global search mode, got %v", m.mode)
	}
	updated, _ := m.updateGlobalSearch(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected Esc to restore edit mode, got %v", m.mode)
	}
	if !m.editor.Focused() {
		t.Fatal("expected editor to be focused after cancel")
	}
}

func TestAutoIndentIncrementsOrderedList(t *testing.T) {
	m, _, _ := openEditModel(t, "ordered")
	m.editor.SetValue("1. first")
	m.editor.SetCursor(8)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editor.Value() != "1. first\n2. " {
		t.Fatalf("expected incremented ordered list, got %q", m.editor.Value())
	}
}

func TestEnterOnEmptyListClearsMarker(t *testing.T) {
	m, _, _ := openEditModel(t, "empty-list")
	m.editor.SetValue("- ")
	m.editor.SetCursor(2)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editor.Value() != "" {
		t.Fatalf("expected empty list marker to be cleared, got %q", m.editor.Value())
	}
}

func TestCheckboxListContinuation(t *testing.T) {
	m, _, _ := openEditModel(t, "checkbox")
	m.editor.SetValue("[ ] task")
	m.editor.SetCursor(8)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editor.Value() != "[ ] task\n[ ] " {
		t.Fatalf("expected checkbox continuation, got %q", m.editor.Value())
	}
}

func TestParseFrontMatterRoundTrip(t *testing.T) {
	content := "---\ntitle: 会议记录\ntags: [work, meeting]\ncreated: 2026-08-14\n---\n\n# 今日会议\n"
	meta, body := parseFrontMatter(content)
	if meta.Title != "会议记录" || meta.Created != "2026-08-14" {
		t.Fatalf("unexpected meta %+v", meta)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "work" || meta.Tags[1] != "meeting" {
		t.Fatalf("unexpected tags %v", meta.Tags)
	}
	if strings.TrimSpace(body) != "# 今日会议" {
		t.Fatalf("unexpected body %q", body)
	}

	if got := writeFrontMatter(body, meta); got != content {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", got, content)
	}

	emptyMeta, emptyBody := parseFrontMatter("# plain\n")
	if len(emptyMeta.Tags) != 0 || emptyMeta.Title != "" {
		t.Fatalf("expected empty meta for plain note, got %+v", emptyMeta)
	}
	if emptyBody != "# plain\n" {
		t.Fatalf("expected unchanged body, got %q", emptyBody)
	}
}

func TestEditTagsWritesFrontMatter(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "tagged")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	if handled, _ := m.globalKey("ctrl+shift+t"); !handled {
		t.Fatal("expected Ctrl+Shift+T to be handled in edit mode")
	}
	if m.mode != modeTag {
		t.Fatalf("expected tag edit mode, got %v", m.mode)
	}
	m.input.SetValue("work, meeting")
	updated, _ := m.updateTagEdit(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected to return to edit mode, got %v", m.mode)
	}

	content, err := store.Read(note)
	if err != nil {
		t.Fatal(err)
	}
	meta, body := parseFrontMatter(content)
	if len(meta.Tags) != 2 || meta.Tags[0] != "work" || meta.Tags[1] != "meeting" {
		t.Fatalf("unexpected saved tags %v", meta.Tags)
	}
	if meta.Title != "tagged" {
		t.Fatalf("expected title derived from filename, got %q", meta.Title)
	}
	if strings.TrimSpace(body) != "# tagged" {
		t.Fatalf("unexpected body %q", body)
	}
	if len(m.nodeTags[note]) != 2 {
		t.Fatalf("expected node tags recorded, got %v", m.nodeTags[note])
	}
	view := stripANSI(m.contentView(100))
	if !strings.Contains(view, "▍ work") || !strings.Contains(view, "▍ meeting") {
		t.Fatalf("expected tags in metadata row, got %q", view)
	}
}

func TestTagFilterShowsOnlyMatchingNotes(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, writeFrontMatter("# Alpha\n\n", FrontMatter{Tags: []string{"work"}})); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, writeFrontMatter("# Beta\n\n", FrontMatter{Tags: []string{"home"}})); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.startTagFilter()
	m.input.SetValue("work")
	updated, _ := m.updateTagFilter(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.tagFilter != "work" {
		t.Fatalf("expected tag filter work, got %q", m.tagFilter)
	}
	if len(m.flat) != 1 || m.flat[0].node.RelPath != noteA {
		t.Fatalf("expected only alpha visible, got %d rows", len(m.flat))
	}
	if view := stripANSI(m.treeView(100)); !strings.Contains(view, "#work") {
		t.Fatalf("expected filter indicator in tree title, got %q", view)
	}

	updated, _ = m.updateTagFilter(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.tagFilter != "" || len(m.flat) != 2 {
		t.Fatalf("expected Esc to clear filter, filter=%q rows=%d", m.tagFilter, len(m.flat))
	}
}

func TestTreeShowsTagCount(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "meeting-notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(note, writeFrontMatter("# Meeting\n\n", FrontMatter{Tags: []string{"work", "meeting"}})); err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	view := stripANSI(m.treeView(100))
	if !strings.Contains(view, "meeting-notes #2") {
		t.Fatalf("expected tag count in tree, got %q", view)
	}
}

func TestCtrlNStartsBlankNote(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)

	if handled, _ := m.globalKey("ctrl+n"); !handled {
		t.Fatal("expected Ctrl+N to be handled")
	}
	if m.mode != modePrompt || m.promptKind != promptNote {
		t.Fatalf("expected note name prompt, mode=%v kind=%v", m.mode, m.promptKind)
	}

	m.input.SetValue("scratch")
	updated, _ := m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected edit mode after creating a note, got %v", m.mode)
	}
	content, err := store.Read(m.currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "# scratch\n\n" {
		t.Fatalf("expected blank note content, got %q", content)
	}
}

func TestSpaceTogglesMultiSelectAndCtrlASelectsAll(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(noteA)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if !m.selectedItems[noteA] {
		t.Fatalf("expected Space to select %q", noteA)
	}
	view := stripANSI(m.treeView(100))
	if !strings.Contains(view, "☑ alpha") || !strings.Contains(view, "○ beta") {
		t.Fatalf("expected selection markers in tree, got %q", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.selectedItems[noteA] {
		t.Fatal("expected second Space to deselect")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = updated.(Model)
	if !m.selectedItems[noteA] || !m.selectedItems[noteB] {
		t.Fatalf("expected Ctrl+A to select all visible notes, got %v", m.selectedItems)
	}
	if m.selectedCount() != 2 {
		t.Fatalf("expected 2 selected, got %d", m.selectedCount())
	}

	m.treeKey("ctrl+shift+a")
	if m.selectedCount() != 0 {
		t.Fatalf("expected Ctrl+Shift+A to clear selection, got %d", m.selectedCount())
	}
}

func TestBatchDelete(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectAllVisible()
	m.startDelete()
	if m.mode != modeConfirm || m.confirmCount != 2 {
		t.Fatalf("expected batch delete confirm, mode=%v count=%d", m.mode, m.confirmCount)
	}
	if view := stripANSI(m.dialogView()); !strings.Contains(view, "将删除 2 个笔记") {
		t.Fatalf("expected batch delete message, got %q", view)
	}

	updated, _ := m.updateConfirm("y")
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected normal mode after delete, got %v", m.mode)
	}
	for _, path := range []string{noteA, noteB} {
		if _, err := os.Stat(filepath.Join(store.Root, path)); !os.IsNotExist(err) {
			t.Fatalf("expected %q deleted, err=%v", path, err)
		}
	}
	if m.selectedCount() != 0 {
		t.Fatalf("expected selection cleared after batch delete, got %d", m.selectedCount())
	}
}

func TestSingleDeleteViaXKey(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	noteC, err := store.CreateNote("", "gamma")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	if len(m.flat) != 3 {
		t.Fatalf("expected 3 flat rows, got %d", len(m.flat))
	}

	// Select the middle note (beta) and press 'x'.
	m.selectPath(noteB)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("expected confirm mode after 'x', got mode=%v", m.mode)
	}
	if m.confirm != "beta.md" || m.confirmCount != 0 || m.confirmDir {
		t.Fatalf("unexpected confirm state: confirm=%q count=%d dir=%v", m.confirm, m.confirmCount, m.confirmDir)
	}
	if view := stripANSI(m.dialogView()); !strings.Contains(view, "Delete \u201cbeta.md\u201d?") {
		t.Fatalf("expected delete confirmation dialog, got %q", view)
	}

	// Confirm the delete.
	updated, _ = m.updateConfirm("y")
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected normal mode after confirm, got %v", m.mode)
	}
	// Note deleted on disk.
	if _, err := os.Stat(filepath.Join(store.Root, noteB)); !os.IsNotExist(err) {
		t.Fatalf("expected %q deleted, err=%v", noteB, err)
	}
	// Tree refreshed: flat list rebuilt without beta.
	if len(m.flat) != 2 {
		t.Fatalf("expected tree refreshed to 2 rows, got %d", len(m.flat))
	}
	var paths []string
	for _, item := range m.flat {
		paths = append(paths, item.node.RelPath)
	}
	if paths[0] != noteA || paths[1] != noteC {
		t.Fatalf("unexpected flat rows after delete: %v", paths)
	}
	// Selection must land on the NEXT item (gamma), not the first item.
	if got := m.selectedPath(); got != noteC {
		t.Fatalf("expected selection on next item %q after delete, got %q", noteC, got)
	}
}

func TestSingleDeleteLastItemClampsSelection(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(noteB) // last item
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(Model)
	updated, _ = m.updateConfirm("y")
	m = updated.(Model)

	if _, err := os.Stat(filepath.Join(store.Root, noteB)); !os.IsNotExist(err) {
		t.Fatalf("expected %q deleted, err=%v", noteB, err)
	}
	if len(m.flat) != 1 {
		t.Fatalf("expected 1 remaining row, got %d", len(m.flat))
	}
	if got := m.selectedPath(); got != noteA {
		t.Fatalf("expected selection clamped to last remaining item %q, got %q", noteA, got)
	}
}

func TestBatchExport(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, "# Alpha\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, "# Beta\n"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectAllVisible()
	if handled, _ := m.globalKey("ctrl+shift+e"); !handled {
		t.Fatal("expected Ctrl+Shift+E to handle batch export")
	}
	if m.mode != modeExport || !m.batchExport || m.exportPath {
		t.Fatalf("expected batch export format menu, mode=%v batch=%v path=%v", m.mode, m.batchExport, m.exportPath)
	}

	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = updated.(Model)
	if !m.batchExport || !m.exportPath || m.exportHTML {
		t.Fatalf("expected Markdown batch export path dialog, batch=%v path=%v html=%v", m.batchExport, m.exportPath, m.exportHTML)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	m.input.SetValue(outDir)
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	data, err := os.ReadFile(filepath.Join(outDir, noteA))
	if err != nil || string(data) != "# Alpha\n" {
		t.Fatalf("alpha export = %q, %v", string(data), err)
	}
	data, err = os.ReadFile(filepath.Join(outDir, noteB))
	if err != nil || string(data) != "# Beta\n" {
		t.Fatalf("beta export = %q, %v", string(data), err)
	}
}

func TestBatchTag(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, "# Alpha\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, "# Beta\n\n"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectAllVisible()
	if handled, _ := m.globalKey("ctrl+shift+t"); !handled {
		t.Fatal("expected Ctrl+Shift+T to handle batch tag")
	}
	if m.mode != modeTag || !m.batchTag {
		t.Fatalf("expected batch tag mode, mode=%v batch=%v", m.mode, m.batchTag)
	}

	m.input.SetValue("work, urgent")
	updated, _ := m.updateTagEdit(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected normal mode after batch tag, got %v", m.mode)
	}
	for _, path := range []string{noteA, noteB} {
		content, err := store.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		meta, _ := parseFrontMatter(content)
		if len(meta.Tags) != 2 || meta.Tags[0] != "work" || meta.Tags[1] != "urgent" {
			t.Fatalf("unexpected tags for %q: %v", path, meta.Tags)
		}
	}
}

func TestExportCreatesDirectories(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "export-dir")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Exported\n")

	m.startExport()
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	m.input.SetValue(filepath.Join("nested", "deep", "copy.md"))
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	want := filepath.Join(store.Root, "nested", "deep", "copy.md")
	data, err := os.ReadFile(want)
	if err != nil || string(data) != "# Exported\n" {
		t.Fatalf("exported content = %q, %v", string(data), err)
	}
}

func TestSortByModifiedAndCreated(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "a-note")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "b-note")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, writeFrontMatter("# A\n\n", FrontMatter{Created: "2020-01-01"})); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, writeFrontMatter("# B\n\n", FrontMatter{Created: "2024-01-01"})); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)

	m.sortMode = sortByCreated
	m.rebuildFlat()
	if m.flat[0].node.RelPath != noteB || m.flat[1].node.RelPath != noteA {
		t.Fatalf("expected newest created first, got %q, %q", m.flat[0].node.RelPath, m.flat[1].node.RelPath)
	}

	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	if err := os.Chtimes(filepath.Join(store.Root, noteA), older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(store.Root, noteB), newer, newer); err != nil {
		t.Fatal(err)
	}
	m.sortMode = sortByModified
	m.rebuildFlat()
	if m.flat[0].node.RelPath != noteB || m.flat[1].node.RelPath != noteA {
		t.Fatalf("expected newest modified first, got %q, %q", m.flat[0].node.RelPath, m.flat[1].node.RelPath)
	}

	m.sortMode = sortByName
	m.rebuildFlat()
	if m.flat[0].node.RelPath != noteA || m.flat[1].node.RelPath != noteB {
		t.Fatalf("expected name sort, got %q, %q", m.flat[0].node.RelPath, m.flat[1].node.RelPath)
	}
}

func TestSearchHighlightCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("needle line\n")
	}
	out := highlightSearchContent(b.String(), "needle")
	count := strings.Count(out, "\x1b[7m")
	if count > maxSearchHighlight {
		t.Fatalf("expected highlights capped at %d, got %d", maxSearchHighlight, count)
	}
	if count != maxSearchHighlight {
		t.Fatalf("expected %d highlights, got %d", maxSearchHighlight, count)
	}
}

func TestAutoSaveSilentOnSuccess(t *testing.T) {
	m, store, note := openEditModel(t, "auto-save")
	m.editor.SetValue("# Auto\n")
	m.lastEditTime = time.Now().Add(-3 * time.Second)
	if !m.dirty() {
		t.Fatal("expected note to be dirty before auto-save")
	}

	updated, _ := m.Update(autoSaveMsg{})
	m = updated.(Model)

	content, err := store.Read(note)
	if err != nil || content != "# Auto\n" {
		t.Fatalf("auto-saved content = %q, %v", content, err)
	}
	if m.dirty() {
		t.Fatal("expected note clean after auto-save")
	}
	if strings.Contains(m.status, "Saved") {
		t.Fatalf("expected silent auto-save, got status %q", m.status)
	}
}

func TestAutoSaveShowsErrorOnFailure(t *testing.T) {
	m, _, _ := openEditModel(t, "auto-fail")
	m.currentPath = "missing/auto.md"
	m.editor.SetValue("content")
	m.lastEditTime = time.Now().Add(-3 * time.Second)

	updated, _ := m.Update(autoSaveMsg{})
	m = updated.(Model)

	if !m.statusErr {
		t.Fatal("expected auto-save error status")
	}
	if !strings.Contains(m.status, "Save failed") {
		t.Fatalf("expected Save failed status, got %q", m.status)
	}
}

func TestAutoSaveNotBeforeTwoSeconds(t *testing.T) {
	m, store, note := openEditModel(t, "auto-soon")
	m.editor.SetValue("# Pending\n")
	m.lastEditTime = time.Now().Add(-time.Second)

	updated, _ := m.Update(autoSaveMsg{})
	m = updated.(Model)

	if !m.dirty() {
		t.Fatal("expected note still dirty when under two seconds")
	}
	content, err := store.Read(note)
	if err != nil || content == "# Pending\n" {
		t.Fatalf("expected note not yet saved, content=%q err=%v", content, err)
	}
}

func TestQQuitBlockedWhenDirty(t *testing.T) {
	m, _, _ := openEditModel(t, "quit-dirty")
	m.sessionPath = filepath.Join(t.TempDir(), "session.json") // never touch the real ~/.tn session
	m.editor.SetValue("# Unsaved\n")
	m.toggleEdit() // leave edit mode; q only quits from normal mode
	if m.mode != modeNormal {
		t.Fatalf("expected normal mode, got %v", m.mode)
	}
	if !m.dirty() {
		t.Fatal("expected note to be dirty")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no quit command while dirty, got %v", cmd)
	}
	if !strings.Contains(m.status, "Save or discard changes before quitting") {
		t.Fatalf("expected discard warning, got %q", m.status)
	}

	if !m.save() {
		t.Fatal("save failed")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected quit command after saving")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("expected tea.QuitMsg, got %T %v", msg, msg)
	}
}

func TestCommandPaletteOpensAndFilters(t *testing.T) {
	m, _, _ := openEditModel(t, "palette")

	if handled, _ := m.globalKey("ctrl+shift+p"); !handled {
		t.Fatal("expected Ctrl+Shift+P to be handled")
	}
	if m.mode != modeCommand {
		t.Fatalf("expected command mode, got %v", m.mode)
	}
	if view := stripANSI(m.commandView()); !strings.Contains(view, "Commands") {
		t.Fatalf("unexpected command view %q", view)
	}

	m.input.SetValue("undo")
	m.commandQuery = "undo"
	cmds := m.filteredCommands()
	if len(cmds) != 1 || cmds[0].name != "Undo" {
		t.Fatalf("expected filter to match Undo, got %+v", cmds)
	}

	updated, _ := m.updateCommand(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected Esc to close command palette, got %v", m.mode)
	}
}

func TestCommandPaletteExecutesAction(t *testing.T) {
	m, _, _ := openEditModel(t, "palette-exec")
	m.editor.SetValue("# Save me\n")

	m.startCommand()
	m.input.SetValue("save note")
	m.commandQuery = "save note"
	updated, _ := m.updateCommand(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.mode != modeEdit {
		t.Fatalf("expected to stay in edit mode after command, got %v", m.mode)
	}
	if m.dirty() {
		t.Fatal("expected save command to persist the note")
	}
}

func TestCommandPaletteNewNote(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	m.startCommand()
	m.input.SetValue("new note")
	m.commandQuery = "new note"
	updated, _ := m.updateCommand(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.mode != modePrompt || m.promptKind != promptNote {
		t.Fatalf("expected New note command to open note prompt, mode=%v kind=%v", m.mode, m.promptKind)
	}
}

func TestHelpOpensAndFilters(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	if handled, _ := m.globalKey("?"); !handled {
		t.Fatal("expected ? to be handled")
	}
	if m.mode != modeHelp {
		t.Fatalf("expected help mode, got %v", m.mode)
	}
	if view := stripANSI(m.helpView()); !strings.Contains(view, "New note") {
		t.Fatalf("expected help to list New note, got %q", view)
	}

	m.input.SetValue("new note")
	m.helpHintQ = "new note"
	m.renderHelpContent()
	if view := stripANSI(m.helpView()); !strings.Contains(view, "New note") {
		t.Fatalf("expected filtered help to include new note, got %q", view)
	}

	updated, _ := m.updateHelp(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected Esc to close help, got %v", m.mode)
	}
}

func TestHelpScrollsWithMouseWheel(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)
	m.startHelp()

	y0 := m.helpHintView.YOffset
	updated, _ := m.updateHelp(tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
		X:      50,
		Y:      15,
	})
	m = updated.(Model)
	if m.helpHintView.YOffset <= y0 {
		t.Fatalf("expected mouse wheel to scroll help, offset=%d before=%d", m.helpHintView.YOffset, y0)
	}
}

func TestTagFilterNewNoteInheritsFilterTag(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)

	m.startTagFilter()
	m.input.SetValue("work")
	updated, _ := m.updateTagFilter(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.tagFilter != "work" || len(m.flat) != 0 {
		t.Fatalf("expected empty work-filtered list, filter=%q rows=%d", m.tagFilter, len(m.flat))
	}
	updated, _ = m.updateTagFilter(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal || m.tagFilter != "work" {
		t.Fatalf("expected filter active after confirm, mode=%v filter=%q", m.mode, m.tagFilter)
	}

	m.startPrompt(promptNote)
	m.input.SetValue("task")
	updated, _ = m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	path := m.currentPath
	if path == "" {
		t.Fatal("expected a new note to be created")
	}
	if len(m.flat) != 1 || m.flat[0].node.RelPath != path {
		t.Fatalf("expected new note visible in filtered list, got %d rows", len(m.flat))
	}
	content, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := parseFrontMatter(content)
	if !containsTag(meta.Tags, "work") {
		t.Fatalf("expected new note tagged work, got %v", meta.Tags)
	}
}

func TestFocusModeCanOpenOtherNoteViaGlobalSearch(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, "alpha body"); err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, "beta body"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(noteA)
	m.openSelectedNote()
	m.toggleFocus()
	if !m.focusing {
		t.Fatal("expected focus mode")
	}

	m.startGlobalSearch()
	m.input.SetValue("beta")
	m.runGlobalSearch("beta")
	if len(m.globalSearchResults) == 0 {
		t.Fatal("expected beta in global search results")
	}
	m.globalSearchIndex = 0
	updated, _ := m.updateGlobalSearch(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.currentPath != noteB {
		t.Fatalf("expected beta to open, got %q", m.currentPath)
	}
	if !m.focusing {
		t.Fatal("expected focus mode to persist after opening another note")
	}
	if view := m.View(); !strings.Contains(view, "Esc 退出专注") {
		t.Fatalf("expected focus view after opening another note, got %q", view)
	}
}

func TestUndoRedoRestoresCursorPosition(t *testing.T) {
	m, _, _ := openEditModel(t, "cursor-undo")
	m.editor.SetValue("hello")
	m.editor.SetCursor(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = updated.(Model)
	if m.editor.Value() != "hello!" {
		t.Fatalf("expected hello!, got %q", m.editor.Value())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	m = updated.(Model)
	pos := m.cursorPos()
	if m.editor.Value() != "hello" || pos.row != 0 || pos.col != 5 {
		t.Fatalf("expected undo cursor at end of hello, value=%q pos=%+v", m.editor.Value(), pos)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	m = updated.(Model)
	pos = m.cursorPos()
	if m.editor.Value() != "hello!" || pos.row != 0 || pos.col != 6 {
		t.Fatalf("expected redo cursor at end of hello!, value=%q pos=%+v", m.editor.Value(), pos)
	}
}

func TestPinTogglePersistsAndSorts(t *testing.T) {
	store := storage.New(t.TempDir())
	alpha, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(beta)
	m.togglePinned()

	content, err := store.Read(beta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "pinned: true") {
		t.Fatalf("expected pinned front matter, got %q", content)
	}
	if !m.nodePinned[beta] {
		t.Fatal("expected beta recorded as pinned")
	}
	if m.flat[0].node.RelPath != beta {
		t.Fatalf("expected pinned note first, got %q", m.flat[0].node.RelPath)
	}
	if view := stripANSI(m.treeView(100)); !strings.Contains(view, "★ beta") {
		t.Fatalf("expected pin marker in tree, got %q", view)
	}

	m.togglePinned()
	content, err = store.Read(beta)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "pinned: true") {
		t.Fatalf("expected unpinned content, got %q", content)
	}
	if m.flat[0].node.RelPath != alpha {
		t.Fatalf("expected name order after unpin, got %q", m.flat[0].node.RelPath)
	}
}

func TestPinShortcutHandledOutsideEditMode(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "pin")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	if handled, _ := m.globalKey("*"); !handled {
		t.Fatal("expected * to be handled in preview mode")
	}
	m.toggleEdit()
	if handled, _ := m.globalKey("*"); handled {
		t.Fatal("expected * to remain a literal character in edit mode")
	}
}

func TestBackForwardHistory(t *testing.T) {
	store := storage.New(t.TempDir())
	alpha, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(alpha)
	m.openSelectedNote()
	m.selectPath(beta)
	m.openSelectedNote()
	if m.currentPath != beta || len(m.history) != 2 || m.historyIndex != 1 {
		t.Fatalf("unexpected history after opening two notes: current=%q history=%v index=%d", m.currentPath, m.history, m.historyIndex)
	}

	if handled, _ := m.globalKey("alt+left"); !handled {
		t.Fatal("expected Alt+Left to be handled")
	}
	if m.currentPath != alpha || m.historyIndex != 0 {
		t.Fatalf("expected back to alpha, got current=%q index=%d", m.currentPath, m.historyIndex)
	}
	if handled, _ := m.globalKey("alt+right"); !handled {
		t.Fatal("expected Alt+Right to be handled")
	}
	if m.currentPath != beta || m.historyIndex != 1 {
		t.Fatalf("expected forward to beta, got current=%q index=%d", m.currentPath, m.historyIndex)
	}
}

func TestGKeyMovesToTreeTopWithoutSelectionMode(t *testing.T) {
	store := storage.New(t.TempDir())
	if _, err := store.CreateNote("", "alpha"); err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(beta)
	if m.selected == 0 {
		t.Fatal("expected beta to be selected")
	}
	if handled, _ := m.globalKey("g"); handled {
		t.Fatal("expected g not to be a global selection shortcut")
	}
	m.active = treePane
	m.treeKey("g")
	if m.selected != 0 {
		t.Fatalf("expected g to jump to tree top, got index %d", m.selected)
	}
}

func TestReadingTimeEstimate(t *testing.T) {
	if got := readingTimeEstimate(""); got != "<1 min" {
		t.Fatalf("expected <1 min for empty note, got %q", got)
	}
	if got := readingTimeEstimate("one two three"); got != "<1 min" {
		t.Fatalf("expected <1 min for short note, got %q", got)
	}
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("word ")
	}
	if got := readingTimeEstimate(b.String()); got != "2 min" {
		t.Fatalf("expected 2 min for 400 words, got %q", got)
	}
	if wordCount("你好 world") <= len(strings.Fields("你好 world")) {
		t.Fatalf("expected CJK characters to count toward words, got %d", wordCount("你好 world"))
	}
}

func TestSearchSnippetShowsMatchWindow(t *testing.T) {
	s := searchSnippet("prefix filler needle tail after", "needle", 20)
	if !strings.Contains(s, "needle") {
		t.Fatalf("expected match in snippet, got %q", s)
	}
	if !strings.HasPrefix(s, "…") {
		t.Fatalf("expected leading context marker, got %q", s)
	}
}

func TestQuickOpenFiltersAndOpens(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "quarterly-report")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNote("", "ideas"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.startQuickOpen()
	if m.mode != modeQuickOpen {
		t.Fatalf("expected quick open mode, got %v", m.mode)
	}

	m.input.SetValue("report")
	m.runQuickOpen("report")
	if len(m.quickOpenResults) != 1 || m.quickOpenResults[0].path != noteA {
		t.Fatalf("expected only %q to match, got %+v", noteA, m.quickOpenResults)
	}

	m.quickOpenIndex = 0
	m.openQuickOpenResult()
	if m.currentPath != noteA {
		t.Fatalf("expected quick open to open %q, got %q", noteA, m.currentPath)
	}
	if len(m.history) != 1 || m.history[0] != noteA {
		t.Fatalf("expected quick open to record history, got %v", m.history)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected preview mode after quick open, got %v", m.mode)
	}
	if m.quickOpenResults != nil || m.input.Focused() {
		t.Fatalf("expected quick open dialog cleared, results=%v focused=%v", m.quickOpenResults, m.input.Focused())
	}
}

func TestQuickOpenEscRestoresMode(t *testing.T) {
	m, _, _ := openEditModel(t, "qo-esc")
	m.startQuickOpen()
	if m.mode != modeQuickOpen {
		t.Fatalf("expected quick open mode, got %v", m.mode)
	}
	updated, _ := m.updateQuickOpen(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected Esc to restore edit mode, got %v", m.mode)
	}
	if !m.editor.Focused() {
		t.Fatal("expected editor to be focused after cancelling quick open")
	}
}

func TestContentMetadataShowsStats(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "stats")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("one two\n\nthree four")
	m.renderMarkdown()

	view := stripANSI(m.contentView(100))
	for _, want := range []string{"4 words", "19 chars", "3 lines", "~<1 min read"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected metadata to contain %q, got %q", want, view)
		}
	}
}

func TestReadingProgressReflectsScroll(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "long-read")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString(fmt.Sprintf("line %d\n\n", i))
	}
	m.editor.SetValue(b.String())
	m.renderMarkdown()
	m.preview.SetYOffset(20)

	pct := m.previewPercent()
	if pct <= 0 || pct >= 100 {
		t.Fatalf("expected in-progress reading percent, got %d", pct)
	}
	m.status, m.statusErr, m.statusOK = "", false, false
	if bar := stripANSI(m.shortcutBar()); !strings.Contains(bar, fmt.Sprintf("%d%%", pct)) {
		t.Fatalf("expected reading percent in status bar, got %q", bar)
	}
	if view := stripANSI(m.contentView(100)); !strings.Contains(view, fmt.Sprintf("%d%%", pct)) {
		t.Fatalf("expected reading percent in content title, got %q", view)
	}
}

func TestTreeMouseWheelStepsByOne(t *testing.T) {
	store := storage.New(t.TempDir())
	for i := 0; i < 35; i++ {
		if _, err := store.CreateNote("", fmt.Sprintf("n%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m := New(store)
	m.resize(100, 30)
	m.treeOffset = 0

	m.handleMouse(tea.MouseEvent{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 3, Y: 2})
	if m.treeOffset != 1 {
		t.Fatalf("expected tree wheel to scroll one row, got %d", m.treeOffset)
	}
	m.handleMouse(tea.MouseEvent{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress, X: 3, Y: 2})
	if m.treeOffset != 0 {
		t.Fatalf("expected tree wheel up to restore offset 0, got %d", m.treeOffset)
	}
}

func TestHTMLExportShortcutSavesStyledPage(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "html-export")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue(writeFrontMatter("# Heading\n\nSome **bold** text.\n", FrontMatter{Title: "My Doc", Tags: []string{"work", "report"}}))

	if handled, _ := m.globalKey("alt+h"); !handled {
		t.Fatal("expected Alt+H to be handled in preview mode")
	}
	if m.mode != modeExport || !m.exportHTML || !m.exportPath {
		t.Fatalf("expected HTML export path dialog, mode=%v html=%v path=%v", m.mode, m.exportHTML, m.exportPath)
	}
	if want := filepath.Join(store.Root, "html-export.html"); m.input.Value() != want {
		t.Fatalf("default HTML path = %q, want %q", m.input.Value(), want)
	}

	m.input.SetValue("out.html")
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{"<title>My Doc</title>", "<span class=\"tag\">work</span>", "<h1>Heading</h1>", "<strong>bold</strong>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected HTML to contain %q, got:\n%s", want, out)
		}
	}
	if m.status != "✓ Exported HTML to "+filepath.Join(store.Root, "out.html") || !m.statusOK {
		t.Fatalf("unexpected export status %q, ok=%v", m.status, m.statusOK)
	}
}

func TestHTMLExportDialogOption3(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "html-option")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue("# Option 3\n")

	m.startExport()
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = updated.(Model)
	if !m.exportHTML || !m.exportPath {
		t.Fatalf("expected option 3 to start HTML path dialog, html=%v path=%v", m.exportHTML, m.exportPath)
	}
	if want := filepath.Join(store.Root, "html-option.html"); m.input.Value() != want {
		t.Fatalf("default HTML path = %q, want %q", m.input.Value(), want)
	}

	m.input.SetValue("opt3.html")
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "opt3.html"))
	if err != nil || !strings.Contains(string(data), "Option 3") {
		t.Fatalf("expected HTML content, got %q, %v", string(data), err)
	}
}

func TestBatchHTMLExport(t *testing.T) {
	store := storage.New(t.TempDir())
	noteA, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteA, "# Alpha\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(noteB, "# Beta\n"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectAllVisible()
	if handled, _ := m.globalKey("ctrl+shift+e"); !handled {
		t.Fatal("expected Ctrl+Shift+E to handle batch export")
	}
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	if !m.batchExport || !m.exportHTML || !m.exportPath {
		t.Fatalf("expected HTML batch path dialog, batch=%v html=%v path=%v", m.batchExport, m.exportHTML, m.exportPath)
	}

	outDir := filepath.Join(t.TempDir(), "html")
	m.input.SetValue(outDir)
	updated, _ = m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected export to finish, mode=%v", m.mode)
	}
	for _, name := range []string{"alpha.html", "beta.html"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "<!DOCTYPE html>") {
			t.Fatalf("expected %s to be HTML, got %q", name, string(data))
		}
	}
}

func TestBuildHTMLExportSanitizesRawHTML(t *testing.T) {
	out, err := buildHTMLExport("note.md", "# Safe\n\n<script>alert(1)</script>\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<script>") {
		t.Fatalf("expected raw script to be sanitized, got:\n%s", out)
	}
	if !strings.Contains(out, "Safe") {
		t.Fatalf("expected rendered content in HTML, got:\n%s", out)
	}
}

func TestRecentNotesOrderAndQuickOpen(t *testing.T) {
	store := storage.New(t.TempDir())
	alpha, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(alpha)
	m.openSelectedNote()
	m.selectPath(beta)
	m.openSelectedNote()
	if len(m.recent) != 2 || m.recent[0] != beta || m.recent[1] != alpha {
		t.Fatalf("unexpected recent order %v", m.recent)
	}

	m.startQuickOpen()
	if len(m.quickOpenResults) != 2 || m.quickOpenResults[0].path != beta || m.quickOpenResults[1].path != alpha {
		t.Fatalf("unexpected recent quick open results %+v", m.quickOpenResults)
	}
	view := stripANSI(m.quickOpenView())
	if !strings.Contains(view, "Recent") {
		t.Fatalf("expected Recent heading in quick open, got %q", view)
	}
}

func TestRecentNotesPersistAcrossSession(t *testing.T) {
	store := storage.New(t.TempDir())
	alpha, err := store.CreateNote("", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateNote("", "beta")
	if err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.sessionPath = filepath.Join(t.TempDir(), "session.json")
	m.resize(100, 30)
	m.selectPath(alpha)
	m.openSelectedNote()
	m.selectPath(beta)
	m.openSelectedNote()
	m.saveSession()

	restored := New(store)
	restored.sessionPath = m.sessionPath
	restored = restored.restoreSession()
	if len(restored.recent) != 2 || restored.recent[0] != beta || restored.recent[1] != alpha {
		t.Fatalf("expected recent list restored, got %v", restored.recent)
	}
}

func TestResultListHomeEndNavigation(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)
	m.quickOpenResults = []quickOpenResult{
		{path: "a.md", title: "a"},
		{path: "b.md", title: "b"},
		{path: "c.md", title: "c"},
	}
	m.quickOpenIndex = 0
	updated, _ := m.updateQuickOpen(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	if m.quickOpenIndex != 2 {
		t.Fatalf("expected End to jump to last quick open result, got %d", m.quickOpenIndex)
	}
	updated, _ = m.updateQuickOpen(tea.KeyMsg{Type: tea.KeyHome})
	m = updated.(Model)
	if m.quickOpenIndex != 0 {
		t.Fatalf("expected Home to jump to first quick open result, got %d", m.quickOpenIndex)
	}
	updated, _ = m.updateQuickOpen(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.quickOpenIndex != 1 {
		t.Fatalf("expected PgDn to move quick open result, got %d", m.quickOpenIndex)
	}

	m.globalSearchResults = []globalSearchResult{
		{path: "a.md", title: "a"},
		{path: "b.md", title: "b"},
	}
	m.globalSearchIndex = 0
	updated, _ = m.updateGlobalSearch(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	if m.globalSearchIndex != 1 {
		t.Fatalf("expected End to jump to last global search result, got %d", m.globalSearchIndex)
	}

	m.startCommand()
	updated, _ = m.updateCommand(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	if m.commandIndex != len(m.filteredCommands())-1 {
		t.Fatalf("expected End to jump to last command, got %d", m.commandIndex)
	}
}

func TestPreviewPageKeysAndFeedback(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "page-keys")
	if err != nil {
		t.Fatal(err)
	}

	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d of the note body", i))
	}

	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)
	m.openSelectedNote()
	m.editor.SetValue(strings.Join(lines, "\n"))
	m.renderMarkdown()
	m.switchToContent()

	if m.preview.YOffset != 0 {
		t.Fatalf("expected preview to start at top, got offset %d", m.preview.YOffset)
	}

	// Visual feedback: progress bar must be present for a long note.
	status := m.readingStatus()
	if !strings.Contains(status, "▱") || !strings.Contains(status, "%") {
		t.Fatalf("expected progress bar in reading status, got %q", status)
	}

	// Space -> page down.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	if m.preview.YOffset == 0 {
		t.Fatal("Space should page down in preview")
	}
	afterSpace := m.preview.YOffset

	// The percent-read feedback must change after paging.
	pctOriginal := m.previewPercent()
	if pctOriginal == 0 {
		t.Fatal("expected non-zero read percentage after paging down")
	}
	if !strings.Contains(m.readingStatus(), "▰") {
		t.Fatalf("expected filled progress bar after paging down, got %q", m.readingStatus())
	}

	// B (shift+b) -> page up.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	m = updated.(Model)
	if m.preview.YOffset >= afterSpace {
		t.Fatalf("B should page up, offset %d -> %d", afterSpace, m.preview.YOffset)
	}

	// b (lowercase) -> page up as well.
	// Page back down first so we're not already at the top.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	if m.preview.YOffset == 0 {
		t.Fatal("Space should page down before testing b")
	}
	beforeB := m.preview.YOffset
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(Model)
	if m.preview.YOffset >= beforeB {
		t.Fatalf("b should page up, offset %d -> %d", beforeB, m.preview.YOffset)
	}

	// F (shift+f) -> page down, and must NOT open search.
	beforeF := m.preview.YOffset
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if m.preview.YOffset <= beforeF {
		t.Fatalf("F should page down, offset %d -> %d", beforeF, m.preview.YOffset)
	}
	if m.mode != modeNormal {
		t.Fatalf("F must not start search, mode=%v", m.mode)
	}

	// Lowercase f keeps its find-in-note meaning (regression).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if m.mode != modeSearch {
		t.Fatalf("f should start search, mode=%v", m.mode)
	}
}
