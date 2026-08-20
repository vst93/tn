package app

import (
	"testing"

	"github.com/vst93/tn/internal/storage"
)

// A note that uses tabs must not look modified the moment it is opened: the
// editor normalizes tabs to spaces, so the clean baseline has to come from the
// editor rather than from the file on disk.
func TestOpeningNoteWithTabsIsNotDirty(t *testing.T) {
	store := storage.New(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote("", "tabbed")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(note, "# Tabbed\n\n```go\nfunc main() {\n\tprintln(1)\n}\n```\n"); err != nil {
		t.Fatal(err)
	}

	m := New(store)
	m.resize(120, 36)
	m.refresh("")
	if !m.openPath(note) {
		t.Fatalf("failed to open %s", note)
	}
	if m.dirty() {
		t.Errorf("note reported unsaved right after opening: baseline=%d editor=%d",
			len(m.original), len(m.editor.Value()))
	}
}
