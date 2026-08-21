package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
)

func pressKey(m Model, k tea.KeyType, runes ...rune) Model {
	msg := tea.KeyMsg{Type: k}
	if len(runes) > 0 {
		msg = tea.KeyMsg{Type: k, Runes: runes}
	}
	nm, _ := m.Update(msg)
	return nm.(Model)
}

// Regression: bubbles caps the textarea at MaxHeight (default 99) rows, after
// which Enter silently stops inserting newlines. The editor must lift that cap.
func TestEnterWorksPastNinetyNineLines(t *testing.T) {
	store := storage.New(t.TempDir())
	note, _ := store.CreateNote("", "long")
	var b strings.Builder
	for i := 0; i < 150; i++ {
		b.WriteString("line\n")
	}
	store.Write(note, b.String())
	m := New(store)
	m.resize(90, 20)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	// Cursor to the very end, then press Enter.
	m.setCursor(m.editor.LineCount()-1, 0)
	before := m.editor.LineCount()
	m = pressKey(m, tea.KeyEnd)
	m = pressKey(m, tea.KeyEnter)
	if got := m.editor.LineCount(); got != before+1 {
		t.Fatalf("Enter past 99 lines: line count %d, want %d", got, before+1)
	}
}

func TestSelectAllAndCut(t *testing.T) {
	store := storage.New(t.TempDir())
	note, _ := store.CreateNote("", "cut")
	store.Write(note, "hello world\nsecond line\n")
	m := New(store)
	m.resize(90, 20)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()

	copied := ""
	m.copier = func(s string) error { copied = s; return nil }

	m.selectAll()
	if got := m.selectionText(); !strings.HasPrefix(got, "hello world") {
		t.Fatalf("select all text = %q", got)
	}

	m.cutSelection()
	if val := m.editor.Value(); strings.Contains(val, "hello") || strings.Contains(val, "second") {
		t.Fatalf("cut left content behind: %q", val)
	}
	if cmd := m.takePending(); cmd != nil {
		cmd() // runs the async clipboard write
	}
	if copied == "" || !strings.Contains(copied, "hello world") {
		t.Fatalf("cut did not copy selection: %q", copied)
	}
	if m.editor.Value() == "" {
		// trailing empty line remains; acceptable
		t.Log("value fully emptied:", m.editor.Value())
	}
}

func TestEditorMouseClickPositionsCursor(t *testing.T) {
	store := storage.New(t.TempDir())
	note, _ := store.CreateNote("", "click")
	store.Write(note, "alpha beta\ngamma delta\n")
	m := New(store)
	m.resize(90, 20)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	m.setCursor(0, 0)

	x0, y0 := m.editorOrigin()
	rows := parseEditorRows(m.editor.View())
	if len(rows) < 2 {
		t.Fatalf("editor rows not parsed: %d", len(rows))
	}
	gutter := rows[1].gutter
	// Click on 'd' of "delta": logical line 1, col 2.
	pos, ok := m.editorPosAt(x0+gutter+2, y0+1)
	if !ok {
		t.Fatal("click inside editor area not mapped")
	}
	if pos.row != 1 || pos.col != 2 {
		t.Fatalf("click mapped to %v, want row=1 col=2", pos)
	}

	// Simulate a full mouse press through Update: col 6 on logical line 0.
	nm, _ := m.Update(tea.MouseMsg(tea.MouseEvent{X: x0 + rows[0].gutter + 6, Y: y0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}))
	m = nm.(Model)
	if p := m.cursorPos(); p.row != 0 || p.col != 6 {
		t.Fatalf("mouse press cursor at %v, want row=0 col=6", p)
	}
	if m.editSel == nil {
		t.Fatal("mouse press should start a selection anchor")
	}
}

func TestDeleteSelectionJoinsLines(t *testing.T) {
	store := storage.New(t.TempDir())
	note, _ := store.CreateNote("", "del")
	store.Write(note, "aaa\nbbb\nccc\n")
	m := New(store)
	m.resize(90, 20)
	m.selectPath(note)
	m.openSelectedNote()
	m.toggleEdit()
	// Select from line 1 col 0 through line 2 col 0 ("bbb\n") and delete it.
	m.editSel = &editorSel{anchor: cursorPos{row: 1, col: 0}, end: cursorPos{row: 2, col: 0}}
	m.deleteSelection()
	if got := m.editor.Value(); got != "aaa\nccc\n" {
		t.Fatalf("after delete: %q", got)
	}
	if p := m.cursorPos(); p.row != 1 || p.col != 0 {
		t.Fatalf("cursor after delete at %v, want row=1 col=0", p)
	}
}
