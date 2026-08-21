package app

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	if out, err := exec.Command("git", "config", "--global", "user.email").Output(); err != nil || len(out) == 0 {
		exec.Command("git", "config", "--global", "user.email", "tn@test").Run()
		exec.Command("git", "config", "--global", "user.name", "tn test").Run()
	}
}

// The ✕ in the tree pane's bottom border closes the list pane.
func TestTreeCloseButtonClosesList(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(90, 20)
	if !m.treeVisible {
		m.toggleTree()
	}
	x, y := m.treeWidth-2, 1 // « button sits in the top border
	m.handleMouse(tea.MouseEvent{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.treeVisible {
		t.Fatal("clicking the ✕ should close the list pane")
	}
	// Clicking elsewhere on the bottom border must NOT close it.
	m.toggleTree()
	m.handleMouse(tea.MouseEvent{X: 5, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !m.treeVisible {
		t.Fatal("clicking away from ✕ must keep the list pane open")
	}
}

// With the pane closed, the ❯ glyph in the header reopens it.
func TestHeaderReopenGlyphOpensList(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(store)
	m.resize(90, 20)
	if m.treeVisible {
		m.toggleTree()
	}
	header := m.headerView()
	if !strings.Contains(stripANSI(header), "»") {
		t.Fatalf("closed list should show a reopen » in the header: %q", stripANSI(header)[:40])
	}
	nm, _ := m.Update(tea.MouseMsg(tea.MouseEvent{X: 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}))
	m = nm.(Model)
	if !m.treeVisible {
		t.Fatal("clicking ❯ should reopen the list pane")
	}
}

// End-to-end git history: commit two revisions of a note, then browse and
// revert from the history view.
func TestGitHistoryAndRevert(t *testing.T) {
	gitAvailable(t)
	store := storage.New(t.TempDir())
	note, _ := store.CreateNote("", "hist")
	store.Write(note, "version one\n")

	config := GitSyncConfig{Branch: "main"}
	if err := saveGitSyncConfig(store.Root, config); err != nil {
		t.Fatal(err)
	}
	if _, err := gitPush(store.Root, config); err != nil {
		t.Fatalf("first push: %v", err)
	}
	store.Write(note, "version two\n")
	if _, err := gitPush(store.Root, config); err != nil {
		t.Fatalf("second push: %v", err)
	}

	m := New(store)
	m.resize(90, 20)
	m.selectPath(note)
	m.openSelectedNote()
	m.startGitHistory()
	if m.mode != modeGitHistory || len(m.gitVersions) < 2 {
		t.Fatalf("expected history mode with ≥2 versions, got %d", len(m.gitVersions))
	}

	// Select the oldest version (last in the newest-first list) and preview it.
	m.gitHistoryIdx = len(m.gitVersions) - 1
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if !m.gitViewing || !strings.Contains(m.gitViewContent, "version one") {
		t.Fatalf("previewing oldest version failed: %q", m.gitViewContent)
	}

	// Enter again rolls back to it.
	nm2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm2.(Model)
	if m.mode == modeGitHistory {
		t.Fatal("revert should close the dialog")
	}
	got, _ := store.Read(note)
	if got != "version one\n" {
		t.Fatalf("file after revert: %q", got)
	}
	if got := m.editor.Value(); got != "version one\n" {
		t.Fatalf("editor after revert: %q", got)
	}
}

func TestGitConfigSaveAndLoad(t *testing.T) {
	store := storage.New(t.TempDir())
	config := GitSyncConfig{Remote: "git@example.com:a/b.git", Branch: "trunk", Author: "N <e@x>", AutoSync: true, AutoMins: 10}
	if err := saveGitSyncConfig(store.Root, config); err != nil {
		t.Fatal(err)
	}
	got := loadGitSyncConfig(store.Root)
	if got.Remote != config.Remote || got.Branch != "trunk" || !got.AutoSync {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// Empty branch falls back to main.
	if err := saveGitSyncConfig(store.Root, GitSyncConfig{}); err != nil {
		t.Fatal(err)
	}
	if got := loadGitSyncConfig(store.Root); got.Branch != "main" {
		t.Fatalf("branch fallback: %q", got.Branch)
	}
}
