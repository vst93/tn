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
	if action := m.toolbarActionAt(11); action != "tagfilter" {
		t.Fatalf("second action = %q, want tagfilter", action)
	}
	if action := m.toolbarActionAt(21); action != "note" {
		t.Fatalf("third action = %q, want note", action)
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
	if strings.Contains(view, "◆ vnote") {
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

func TestNewNoteTemplateFlow(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)

	if handled, _ := m.globalKey("ctrl+n"); !handled {
		t.Fatal("expected Ctrl+N to be handled")
	}
	if m.mode != modeTemplate {
		t.Fatalf("expected template mode, got %v", m.mode)
	}
	if view := stripANSI(m.templateView()); !strings.Contains(view, "选择模板") || !strings.Contains(view, "4. 读书笔记") {
		t.Fatalf("unexpected template view %q", view)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	if m.templateIndex != 1 {
		t.Fatalf("expected daily template selected, got %d", m.templateIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modePrompt || m.promptKind != promptNote {
		t.Fatalf("expected note name prompt, mode=%v kind=%v", m.mode, m.promptKind)
	}

	m.input.SetValue("standup")
	updated, _ = m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeEdit {
		t.Fatalf("expected edit mode after creating note, got %v", m.mode)
	}
	content, err := store.Read(m.currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if content != noteTemplates["daily"] {
		t.Fatalf("expected daily template content, got %q", content)
	}
	if m.pendingTemplate != "" {
		t.Fatalf("expected pending template to be consumed, got %q", m.pendingTemplate)
	}
}

func TestBlankTemplateUsesTitle(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(100, 30)
	m.startTemplate()
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue("scratch")
	updated, _ := m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	content, err := store.Read(m.currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "# scratch\n\n" {
		t.Fatalf("expected blank template with title, got %q", content)
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
	if m.mode != modeExport || !m.batchExport || !m.exportPath {
		t.Fatalf("expected batch export path dialog, mode=%v batch=%v path=%v", m.mode, m.batchExport, m.exportPath)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	m.input.SetValue(outDir)
	updated, _ := m.updateExport(tea.KeyMsg{Type: tea.KeyEnter})
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

	if m.mode != modeTemplate {
		t.Fatalf("expected New note command to open template, got %v", m.mode)
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
	if view := stripANSI(m.helpView()); !strings.Contains(view, "Command palette") {
		t.Fatalf("expected help to list command palette, got %q", view)
	}

	m.input.SetValue("command")
	m.helpHintQ = "command"
	m.renderHelpContent()
	if view := stripANSI(m.helpView()); !strings.Contains(view, "Command palette") {
		t.Fatalf("expected filtered help to include command, got %q", view)
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
