package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
)

// Pressing 'n' with a folder selected must create the note inside that folder,
// not at the root.
func TestNewNoteGoesIntoSelectedFolder(t *testing.T) {
	store := storage.New(t.TempDir())
	if _, err := store.CreateDir("", "docs"); err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.refresh("")

	m.selectPath("docs")
	if !m.flat[m.selected].node.IsDir {
		t.Fatalf("expected 'docs' to be selected as a dir")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m.input.SetValue("inside-docs")
	updated, _ = m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.currentPath != "docs/inside-docs.md" {
		t.Fatalf("expected note inside docs folder, got %q", m.currentPath)
	}
	if _, err := store.Read(m.currentPath); err != nil {
		t.Fatalf("read note %q: %v", m.currentPath, err)
	}
}

// The same must hold when the selected folder is collapsed (its children are
// not in the flat list) - the note is still created inside it.
func TestNewNoteGoesIntoCollapsedSelectedFolder(t *testing.T) {
	store := storage.New(t.TempDir())
	if _, err := store.CreateDir("", "docs"); err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.refresh("")

	m.selectPath("docs")
	m.activateSelected() // collapses the folder, selection stays on it

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m.input.SetValue("collapsed-inner")
	updated, _ = m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.currentPath != "docs/collapsed-inner.md" {
		t.Fatalf("expected note inside collapsed docs folder, got %q", m.currentPath)
	}
	if _, err := store.Read(m.currentPath); err != nil {
		t.Fatalf("read note %q: %v", m.currentPath, err)
	}
}

// With a note inside a folder selected, 'n' creates a sibling in that same
// folder rather than at the root.
func TestNewNoteIsSiblingOfSelectedNote(t *testing.T) {
	store := storage.New(t.TempDir())
	if _, err := store.CreateDir("", "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNote("docs", "existing"); err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.refresh("")

	m.selectPath("docs/existing.md")
	if m.flat[m.selected].node.IsDir {
		t.Fatalf("expected note row selected")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m.input.SetValue("sibling")
	updated, _ = m.updatePrompt(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.currentPath != "docs/sibling.md" {
		t.Fatalf("expected sibling note in docs folder, got %q", m.currentPath)
	}
	if _, err := store.Read(m.currentPath); err != nil {
		t.Fatalf("read note %q: %v", m.currentPath, err)
	}
}
