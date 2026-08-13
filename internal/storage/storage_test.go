package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}

	dir, err := s.CreateDir("", "projects")
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateNote(dir, "roadmap")
	if err != nil {
		t.Fatal(err)
	}
	if note != filepath.Join("projects", "roadmap.md") {
		t.Fatalf("unexpected note path %q", note)
	}
	if err := s.Write(note, "# Roadmap\n\n- ship\n"); err != nil {
		t.Fatal(err)
	}
	content, err := s.Read(note)
	if err != nil || content != "# Roadmap\n\n- ship\n" {
		t.Fatalf("read got %q, %v", content, err)
	}

	renamed, err := s.Rename(note, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if renamed != filepath.Join("projects", "plan.md") {
		t.Fatalf("unexpected renamed path %q", renamed)
	}

	tree, err := s.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || !tree[0].IsDir || len(tree[0].Children) != 1 || tree[0].Children[0].RelPath != renamed {
		t.Fatalf("unexpected tree: %#v", tree)
	}
	if err := s.Delete(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, dir)); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestStoreRejectsUnsafePaths(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNote("", "../escape"); err == nil {
		t.Fatal("expected unsafe name error")
	}
	if err := s.Write(filepath.Join("..", "escape.md"), "bad"); err == nil {
		t.Fatal("expected unsafe path error")
	}
	if _, err := s.CreateDir("", "a/b"); err == nil {
		t.Fatal("expected separator error")
	}
}

func TestTreeSortsDirectoriesBeforeNotesAndHidesOtherFiles(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "ignored.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".hidden.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNote("", "zeta"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDir("", "Alpha"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Name != "Alpha" || tree[1].Name != "zeta.md" {
		t.Fatalf("unexpected tree order: %#v", tree)
	}
}
