package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Node is a directory or Markdown note in the note tree.
type Node struct {
	Name     string
	RelPath  string
	IsDir    bool
	Children []*Node
}

// Store persists notes as ordinary directories and .md files.
type Store struct {
	Root string
}

// DefaultRoot returns the platform-specific user home with .tn appended.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return filepath.Join(home, ".tn"), nil
}

func New(root string) *Store {
	return &Store{Root: filepath.Clean(root)}
}

func (s *Store) Init() error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return fmt.Errorf("create note directory: %w", err)
	}
	return nil
}

// Tree reads directories and Markdown files. Other files stay untouched and hidden.
func (s *Store) Tree() ([]*Node, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s.readDir("")
}

func (s *Store) readDir(rel string) ([]*Node, error) {
	path, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", rel, err)
	}

	nodes := make([]*Node, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := filepath.Join(rel, name)
		if entry.IsDir() {
			children, readErr := s.readDir(childRel)
			if readErr != nil {
				return nil, readErr
			}
			nodes = append(nodes, &Node{Name: name, RelPath: childRel, IsDir: true, Children: children})
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".md") {
			nodes = append(nodes, &Node{Name: name, RelPath: childRel})
		}
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes, nil
}

func (s *Store) Read(rel string) (string, error) {
	path, err := s.resolveNote(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read note: %w", err)
	}
	return string(data), nil
}

func (s *Store) Write(rel, content string) error {
	path, err := s.resolveNote(rel)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("save note: %w", err)
	}
	return nil
}

func (s *Store) CreateNote(parent, name string) (string, error) {
	name, err := validName(name)
	if err != nil {
		return "", err
	}
	if filepath.Ext(name) == "" {
		name += ".md"
	}
	if !strings.EqualFold(filepath.Ext(name), ".md") {
		return "", errors.New("note name must end in .md")
	}
	return s.create(parent, name, false)
}

func (s *Store) CreateDir(parent, name string) (string, error) {
	name, err := validName(name)
	if err != nil {
		return "", err
	}
	return s.create(parent, name, true)
}

func (s *Store) create(parent, name string, directory bool) (string, error) {
	parentPath, err := s.resolve(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parentPath)
	if err != nil || !info.IsDir() {
		return "", errors.New("parent directory does not exist")
	}
	rel := filepath.Join(parent, name)
	path, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return "", errors.New("an item with that name already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check destination: %w", err)
	}
	if directory {
		if err := os.Mkdir(path, 0o755); err != nil {
			return "", fmt.Errorf("create directory: %w", err)
		}
	} else if err := os.WriteFile(path, []byte("# "+strings.TrimSuffix(name, filepath.Ext(name))+"\n\n"), 0o644); err != nil {
		return "", fmt.Errorf("create note: %w", err)
	}
	return rel, nil
}

// MoveNode reorders a node within its parent's children.
// direction: -1 moves up, +1 moves down. Returns false if move not possible.
func (s *Store) MoveNode(rel string, direction int) (string, bool, error) {
	if rel == "" || rel == "." {
		return "", false, nil
	}
	parentDir := filepath.Dir(rel)
	if parentDir == "." {
		parentDir = ""
	}
	children, err := s.readDir(parentDir)
	if err != nil {
		return "", false, err
	}
	// Find current index
	idx := -1
	for i, c := range children {
		if c.RelPath == rel {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false, nil
	}
	newIdx := idx + direction
	if newIdx < 0 || newIdx >= len(children) {
		return "", false, nil
	}
	// Swap by renaming to temp then to target order
	// We use a simple approach: rename current to temp, then rename target to current, then temp to target
	current := children[idx]
	target := children[newIdx]

	// Generate unique temp name
	tempName := fmt.Sprintf(".tn_move_%d", time.Now().UnixNano())
	tempRel := filepath.Join(parentDir, tempName)

	oldPath, err := s.resolve(current.RelPath)
	if err != nil {
		return "", false, err
	}
	tempPath, err := s.resolve(tempRel)
	if err != nil {
		return "", false, err
	}
	newPath, err := s.resolve(target.RelPath)
	if err != nil {
		return "", false, err
	}

	// 1. Rename current to temp
	if err := os.Rename(oldPath, tempPath); err != nil {
		return "", false, fmt.Errorf("move item: %w", err)
	}
	// 2. Rename target to current's old path
	if err := os.Rename(newPath, oldPath); err != nil {
		// Rollback: rename temp back to current
		os.Rename(tempPath, oldPath)
		return "", false, fmt.Errorf("move item: %w", err)
	}
	// 3. Rename temp to target's old path
	if err := os.Rename(tempPath, newPath); err != nil {
		// Rollback best-effort
		os.Rename(oldPath, newPath)
		os.Rename(tempPath, oldPath)
		return "", false, fmt.Errorf("move item: %w", err)
	}

	return target.RelPath, true, nil
}

// Rename renames an item in place and returns its new relative path.
func (s *Store) Rename(rel, name string) (string, error) {
	name, err := validName(name)
	if err != nil {
		return "", err
	}
	oldPath, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		return "", fmt.Errorf("find item: %w", err)
	}
	if !info.IsDir() {
		if filepath.Ext(name) == "" {
			name += ".md"
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			return "", errors.New("note name must end in .md")
		}
	}
	newRel := filepath.Join(filepath.Dir(rel), name)
	if filepath.Dir(rel) == "." {
		newRel = name
	}
	newPath, err := s.resolve(newRel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(newPath); err == nil {
		return "", errors.New("an item with that name already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check destination: %w", err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", fmt.Errorf("rename item: %w", err)
	}
	return newRel, nil
}

func (s *Store) Delete(rel string) error {
	if rel == "" || rel == "." {
		return errors.New("cannot delete note root")
	}
	path, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
}

func (s *Store) resolveNote(rel string) (string, error) {
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return "", errors.New("only Markdown notes can be opened")
	}
	return s.resolve(rel)
}

func (s *Store) resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes note directory")
	}
	path := filepath.Join(s.Root, clean)
	relToRoot, err := filepath.Rel(s.Root, path)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes note directory")
	}
	return path, nil
}

// Resolve maps a relative path to an absolute path within the note tree.
func (s *Store) Resolve(rel string) (string, error) {
	return s.resolve(rel)
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", errors.New("name cannot be empty")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) {
		return "", errors.New("name cannot contain path separators")
	}
	if strings.ContainsRune(name, 0) {
		return "", errors.New("name contains an invalid character")
	}
	return name, nil
}
