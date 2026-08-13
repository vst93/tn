package app

import (
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
	if copied != "# Unsaved selection\n" {
		t.Fatalf("copied %q", copied)
	}
	if m.status != "Copied Markdown to clipboard" || m.statusErr {
		t.Fatalf("unexpected copy status %q, error=%v", m.status, m.statusErr)
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
