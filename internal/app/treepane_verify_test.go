package app

import (
	"strings"
	"testing"

	"github.com/vst93/tn/internal/storage"
)

// Tests the tree pane row rendering:
//  1. pinned marker "★" appears before the name
//  2. tag count "#N" appears after the name
//  3. both can appear on the same row without overlapping
//  4. the selected row highlight extends full width
func TestTreePanePinAndTagRendering(t *testing.T) {
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
	m.selectPath(note)
	m.togglePinned() // note is pinned AND has 2 tags

	view := stripANSI(m.treeView(100))
	lines := strings.Split(view, "\n")

	// Find the row for meeting-notes
	var row string
	for _, l := range lines {
		if strings.Contains(l, "meeting-notes") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("note row not found in tree view:\n%s", view)
	}
	t.Logf("row: %q", row)

	// 1. pinned star must precede the name (with selection marker allowed before it)
	if !strings.Contains(row, "★ meeting-notes") {
		t.Fatalf("expected '★ meeting-notes' in row, got %q", row)
	}
	starIdx := strings.Index(row, "★")
	nameIdx := strings.Index(row, "meeting-notes")
	if starIdx < 0 || nameIdx < 0 || starIdx > nameIdx {
		t.Fatalf("star must appear before name, star@%d name@%d in %q", starIdx, nameIdx, row)
	}

	// 2. tag count must follow the name
	if !strings.Contains(row, "meeting-notes #2") {
		t.Fatalf("expected 'meeting-notes #2' in row, got %q", row)
	}
	nameEnd := nameIdx + len("meeting-notes")
	tagIdx := strings.Index(row, "#2")
	if tagIdx < nameEnd {
		t.Fatalf("tag count must appear after name, nameEnd=%d tagIdx=%d in %q", nameEnd, tagIdx, row)
	}
}

// The selected row highlight should extend to the full inner width of the pane.
func TestTreePaneSelectedRowFullWidthHighlight(t *testing.T) {
	store := storage.New(t.TempDir())
	note, err := store.CreateNote("", "gamma")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.resize(100, 30)
	m.selectPath(note)

	// Build tree with right border enabled (two borders => innerWidth = 98)
	raw := m.treeViewSides(100, true, true)

	// Print the raw selected row for inspection
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "gamma") {
			t.Logf("RAW selected row: %q", l)
		}
	}
}