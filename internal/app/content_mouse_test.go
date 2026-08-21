package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
)

// openNoteForDrag opens a single note in preview mode (like a real user
// clicking it in the tree) so the content pane shows rendered markdown.
func openNoteForDrag(t *testing.T, content string) Model {
	t.Helper()
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "drag")
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
	if m.currentPath != note {
		t.Fatalf("expected note %q, got %q", note, m.currentPath)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected preview mode, got %v", m.mode)
	}
	m.editor.SetValue(content)
	m.renderMarkdown()
	if m.renderedPlain == "" {
		t.Fatal("expected renderedPlain to be non-empty")
	}
	return m
}

// TestContentDragSelectCopies verifies that pressing in the content pane,
// dragging, and releasing copies the spanned plain-text span and clears the
// selection state.
func TestContentDragSelectCopies(t *testing.T) {
	m := openNoteForDrag(t, "# Hi there\n\nSome paragraph for selection.")
	copied := ""
	m.copier = func(s string) error {
		copied = s
		return nil
	}

	// The pane's top border is at Y=1 and the metadata line at Y=2 (no
	// tags), so the first preview line renders at Y=3. The content area
	// starts at X = treeWidth+2: the separator column plus the one column of
	// padding the panel adds inside its frame.
	startX := m.treeWidth + 2
	m.handleMouse(tea.MouseEvent{X: startX, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !m.contentDragging {
		t.Fatal("expected contentDragging after press on preview text")
	}
	if m.contentSelAnchor != m.contentSelEnd {
		t.Fatal("expected anchor and end equal after press")
	}

	m.handleMouse(tea.MouseEvent{X: startX + 5, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	if m.contentSelEnd <= m.contentSelAnchor {
		t.Fatal("expected drag motion to advance selection end")
	}
	// Capture the expected span before release clears the state.
	anchor, end := m.contentSelAnchor, m.contentSelEnd

	m.handleMouse(tea.MouseEvent{X: startX + 5, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	if m.contentDragging {
		t.Fatal("expected drag to end after release")
	}
	if m.contentSelAnchor != 0 || m.contentSelEnd != 0 {
		t.Fatal("expected selection state cleared after release")
	}
	// Release schedules an async copy; run it like the message loop would.
	if cmd := m.takePending(); cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	want := string([]rune(m.renderedPlain)[anchor:end])
	if copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
}

// TestContentDragIgnoresEditMode verifies drag selection does not engage in
// edit mode.
func TestContentDragIgnoresEditMode(t *testing.T) {
	m := openNoteForDrag(t, "# Hi there\n\nSome paragraph for selection.")
	m.toggleEdit()
	if m.mode != modeEdit {
		t.Fatalf("expected edit mode, got %v", m.mode)
	}
	m.handleMouse(tea.MouseEvent{X: m.treeWidth + 2, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.contentDragging {
		t.Fatal("expected no drag selection in edit mode")
	}
}

// TestContentDragCopyDisabledForEmptySelection verifies a click without drag
// does not copy anything.
func TestContentDragCopyDisabledForEmptySelection(t *testing.T) {
	m := openNoteForDrag(t, "# Hi there\n\nSome paragraph for selection.")
	copied := "sentinel"
	m.copier = func(s string) error {
		copied = s
		return nil
	}
	m.handleMouse(tea.MouseEvent{X: m.treeWidth + 2, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.handleMouse(tea.MouseEvent{X: m.treeWidth + 2, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	if m.contentDragging || m.contentSelAnchor != 0 || m.contentSelEnd != 0 {
		t.Fatalf("expected drag cleared, got dragging=%v anchor=%d end=%d", m.contentDragging, m.contentSelAnchor, m.contentSelEnd)
	}
	if copied != "sentinel" {
		t.Fatalf("expected no copy for zero-length selection, got %q", copied)
	}
}

// TestContentDragOutsidePaneStartsNothing verifies clicking the tree area or
// outside the pane does not start a drag.
func TestContentDragOutsidePaneStartsNothing(t *testing.T) {
	m := openNoteForDrag(t, "# Hi there\n\nSome paragraph for selection.")

	// Tree pane click: must not start a content drag.
	m.handleMouse(tea.MouseEvent{X: 3, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.contentDragging {
		t.Fatal("expected no drag when pressing in tree pane")
	}

	// Click on the divider column: it belongs to the tree pane, so it must
	// not start a content drag.
	m.handleMouse(tea.MouseEvent{X: m.treeWidth, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.contentDragging {
		t.Fatal("expected no drag when pressing on the divider")
	}

	// Click in the pane's top border row.
	m.handleMouse(tea.MouseEvent{X: m.treeWidth + 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.contentDragging {
		t.Fatal("expected no drag when pressing on top border")
	}
}

// TestContentDragMultiLineSelection verifies dragging across two preview
// lines copies text spanning the newline. Separate paragraphs render on
// separate output lines (glamour merges consecutive source lines).
func TestContentDragMultiLineSelection(t *testing.T) {
	m := openNoteForDrag(t, "line one\n\nline two\n\nline three")
	copied := ""
	m.copier = func(s string) error {
		copied = s
		return nil
	}
	startX := m.treeWidth + 2 // separator column + panel padding inside the frame
	// Each paragraph renders one row: first at Y=3 (below the metadata
	// line), second at Y=4, third at Y=5.
	m.handleMouse(tea.MouseEvent{X: startX, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.handleMouse(tea.MouseEvent{X: startX + 20, Y: 5, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	if m.contentSelEnd <= m.contentSelAnchor {
		t.Fatal("expected multi-line motion to advance selection end")
	}
	anchor, end := m.contentSelAnchor, m.contentSelEnd
	m.handleMouse(tea.MouseEvent{X: startX + 20, Y: 5, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	if cmd := m.takePending(); cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	want := string([]rune(m.renderedPlain)[anchor:end])
	if copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	if strings.Count(copied, "\n") == 0 {
		t.Fatalf("expected multi-line copied text, got %q", copied)
	}
}

// TestContentDragAfterScroll verifies selection mapping accounts for the
// preview viewport's scrolled offset: dragging near the top of the pane
// after scrolling selects later text, not the first rendered line.
func TestContentDragAfterScroll(t *testing.T) {
	// A tall document so scrolling is possible; the pane shows 23 preview
	// rows. Scroll down a bit so the first visible text is not the first
	// rendered line.
	var doc strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&doc, "paragraph number %02d\n\n", i)
	}
	m := openNoteForDrag(t, doc.String())
	m.preview.SetYOffset(10)
	if m.preview.YOffset != 10 {
		t.Fatalf("expected YOffset 10, got %d", m.preview.YOffset)
	}
	copied := ""
	m.copier = func(s string) error {
		copied = s
		return nil
	}
	startX := m.treeWidth + 2 // separator column + panel padding inside the frame
	// The first visible line is renderedPlain line 10 (each paragraph is
	// one line); it must map to an offset starting at that line, not line
	// 0. The anchor should therefore sit inside paragraph 10's text.
	expectStart := 0
	lines := strings.Split(m.renderedPlain, "\n")
	for i := 0; i < 10; i++ {
		expectStart += len([]rune(lines[i])) + 1
	}
	expectEnd := expectStart + len([]rune(lines[10]))
	m.handleMouse(tea.MouseEvent{X: startX, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !m.contentDragging {
		t.Fatal("expected drag after press on first visible scrolled line")
	}
	if m.contentSelAnchor < expectStart || m.contentSelAnchor > expectEnd {
		t.Fatalf("expected anchor inside scrolled line 10 span [%d,%d), got %d", expectStart, expectEnd, m.contentSelAnchor)
	}
	m.handleMouse(tea.MouseEvent{X: startX + 8, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	m.handleMouse(tea.MouseEvent{X: startX + 8, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	if cmd := m.takePending(); cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	if !strings.Contains(copied, "paragrap") {
		t.Fatalf("expected copied text to start in visible scrolled paragraph, got %q", copied)
	}
	if strings.Contains(copied, "paragraph number 00") {
		t.Fatalf("expected copied text not to include scrolled-out first paragraph, got %q", copied)
	}
}

// TestSelectionHighlightClosesAtLineEnd verifies that when a drag selection
// extends to the end of a rendered line, the highlight reset code is emitted
// so that the selection color does not bleed into adjacent panels.
func TestSelectionHighlightClosesAtLineEnd(t *testing.T) {
	m := openNoteForDrag(t, "line one\n\nline two\n\nline three")
	m.copier = func(s string) error { return nil }
	startX := m.treeWidth + 2 // separator column + panel padding inside the frame
	m.handleMouse(tea.MouseEvent{X: startX, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.handleMouse(tea.MouseEvent{X: startX + 100, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	// Capture offsets before release clears them.
	anchor, end := m.contentSelAnchor, m.contentSelEnd
	m.handleMouse(tea.MouseEvent{X: startX + 100, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	if m.contentDragging {
		t.Fatal("expected drag to end after release")
	}
	hi, hiEnd := selectionHighlightCodes()
	if hi == "" {
		t.Skip("no highlight codes in this terminal profile")
	}
	rendered := renderSelectionContent(m.renderedContent, m.renderedPlain, anchor, end)
	lines := strings.Split(rendered, "\n")
	if !strings.Contains(lines[0], hi) {
		t.Fatalf("expected highlight in first line, got %q", lines[0])
	}
	trimmed := strings.TrimRight(lines[0], " ")
	if !strings.HasSuffix(trimmed, hiEnd) {
		t.Fatalf("expected first line to end with highlight reset to prevent bleed, got %q", lines[0])
	}
}
