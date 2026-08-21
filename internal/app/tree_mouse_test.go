package app

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/storage"
)

// clickTree clicks a mouse position in the tree pane like a real user would.
func clickTree(m *Model, x, y int) {
	m.handleMouse(tea.MouseEvent{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

// TestTreeMouseClickSelectsRow verifies that clicking a tree item at the
// first, middle, and last rendered positions selects that exact item, both
// at the top of the tree and after scrolling with treeOffset.
func TestTreeMouseClickSelectsRow(t *testing.T) {
	store := storage.New(t.TempDir())
	for i := 0; i < 30; i++ {
		if _, err := store.CreateNote("", fmt.Sprintf("n%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m := New(store)
	m.resize(100, 30)
	if m.compact {
		t.Fatal("expected split layout at width 100")
	}
	if len(m.flat) != 30 {
		t.Fatalf("expected 30 flat rows, got %d", len(m.flat))
	}

	// Layout invariants at 100x30.
	if m.bodyHeight != 28 {
		t.Fatalf("bodyHeight = %d, want 28", m.bodyHeight)
	}
	treeRows := m.treeRows() // 26

	// Switch to the tree pane so file clicks don't jump to content.
	m.switchToTree()

	// First item: pane title row is Y=1, first item renders at Y=2.
	clickTree(&m, 3, 2)
	if m.selected != 0 {
		t.Fatalf("click first item: selected = %d, want 0", m.selected)
	}

	// Middle of the visible list.
	middle := treeRows / 2
	clickTree(&m, 3, 2+middle)
	if m.selected != middle {
		t.Fatalf("click middle item: selected = %d, want %d", m.selected, middle)
	}

	// Last visible item renders at Y = 2 + treeRows - 1.
	clickTree(&m, 3, 2+treeRows-1)
	if m.selected != treeRows-1 {
		t.Fatalf("click last visible item: selected = %d, want %d", m.selected, treeRows-1)
	}

	// Last item of the whole list (index 29) must be reachable after
	// scrolling to the bottom.
	m.treeOffset = min(len(m.flat)-treeRows, m.treeOffset+treeRows)
	lastIdx := len(m.flat) - 1
	lastY := 2 + (lastIdx - m.treeOffset)
	if lastY > 2+treeRows-1 {
		t.Fatalf("test setup: last item not visible at Y=%d", lastY)
	}
	clickTree(&m, 3, lastY)
	if m.selected != lastIdx {
		t.Fatalf("click last item after scroll: selected = %d, want %d", m.selected, lastIdx)
	}

	// Scrolled by one: first visible item is flat[1] and renders at Y=2.
	m.treeOffset = 1
	clickTree(&m, 3, 2)
	if m.selected != 1 {
		t.Fatalf("click first visible after scroll: selected = %d, want 1", m.selected)
	}
}

// TestTreeMouseClickBelowVisibleRowSelectsNothing verifies that clicking the
// pane's bottom border row (Y == bodyHeight) must not select the hidden item
// just past the visible window.
func TestTreeMouseClickBelowVisibleRowSelectsNothing(t *testing.T) {
	store := storage.New(t.TempDir())
	for i := 0; i < 30; i++ {
		if _, err := store.CreateNote("", fmt.Sprintf("n%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m := New(store)
	m.resize(100, 30)
	m.switchToTree()
	m.selected = -1

	// A click below the visible window (beyond the body area) must be ignored.
	clickTree(&m, 3, m.bodyHeight+1)
	if m.selected != -1 {
		t.Fatalf("click below visible rows: selected = %d, want -1", m.selected)
	}
}
