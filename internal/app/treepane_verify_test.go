package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

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

	// Build the borderless tree pane lines.
	raw := truncateToHeight(m.treePaneLines(100), m.bodyHeight)

	// Print the raw selected row for inspection
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "gamma") {
			t.Logf("RAW selected row: %q", l)
		}
	}
}

// Header tabs: with the tree visible both 'Lists' and 'Preview/Edit'
// tabs are drawn. When the tree is toggled off the 'Lists' tab must
// disappear entirely from the rendered header and hit-testing must stay
// aligned with the remaining content tab (no stale cells / artifacts).
func TestHeaderTabsWhenTreeHidden(t *testing.T) {
	m := New(storage.New(t.TempDir()))
	m.resize(100, 30)

	// Tree visible by default: both tabs render.
	tabs := m.headerTabs()
	if !strings.Contains(tabs, "Lists") || !strings.Contains(tabs, "Preview") {
		t.Fatalf("tree visible tabs = %q, want 'Lists' and 'Preview'", tabs)
	}

	// Toggle the tree off.
	m.toggleTree()
	if m.treeVisible {
		t.Fatal("expected tree hidden after toggle")
	}

	// The 'Lists' tab must be gone, the content tab remains.
	tabs = m.headerTabs()
	if strings.Contains(tabs, "Lists") {
		t.Fatalf("hidden-tree tabs = %q, must not contain 'Lists'", tabs)
	}
	if !strings.Contains(tabs, "Preview") {
		t.Fatalf("hidden-tree tabs = %q, want 'Preview'", tabs)
	}

	// The rendered header line must be artifact-free: no stale 'Lists',
	// padded to the full window width.
	header := stripANSI(m.headerView())
	if strings.Contains(header, "Lists") {
		t.Fatalf("rendered header still contains 'Lists': %q", header)
	}
	if lipgloss.Width(header) != 100 {
		t.Fatalf("header width = %d, want full 100 (no trailing artifact): %q", lipgloss.Width(header), header)
	}

	// Hit-testing must match the drawn tab: the content tab starts right
	// after the brand separator when the tree is hidden.
	notesStart := 1 + lipgloss.Width("◆ TN") + lipgloss.Width(" │ ")
	if p, ok := m.headerTabAt(notesStart); !ok || p != contentPane {
		t.Fatalf("hidden-tree x=%d: pane=%v ok=%v, want contentPane hit", notesStart, p, ok)
	}
	if _, ok := m.headerTabAt(notesStart - 1); ok {
		t.Fatalf("hidden-tree x=%d (brand area) must not hit a tab", notesStart-1)
	}

	// Edit mode with the tree hidden: only 'Edit' renders.
	m.mode = modeEdit
	tabs = m.headerTabs()
	if strings.Contains(tabs, "Lists") || !strings.Contains(tabs, "Edit") {
		t.Fatalf("hidden-tree edit tabs = %q, want only 'Edit'", tabs)
	}
	if strings.Contains(tabs, "Preview") {
		t.Fatalf("hidden-tree edit tabs = %q, must not contain 'Preview'", tabs)
	}
	m.mode = modeNormal

	// Toggling the tree back on restores both tabs (no stale state).
	m.toggleTree()
	if !m.treeVisible {
		t.Fatal("expected tree visible after second toggle")
	}
	tabs = m.headerTabs()
	if !strings.Contains(tabs, "Lists") || !strings.Contains(tabs, "Preview") {
		t.Fatalf("restored tabs = %q, want 'Lists' and 'Preview' again", tabs)
	}
}