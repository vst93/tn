package app

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupManifest is stored inside each backup zip to describe its contents.
type backupManifest struct {
	Version   string            `json:"version"`
	CreatedAt string            `json:"createdAt"`
	Notes     []backupNoteEntry `json:"notes"`
	Tags      map[string][]string `json:"tags,omitempty"`
	Pinned    map[string]bool   `json:"pinned,omitempty"`
}

type backupNoteEntry struct {
	RelPath  string `json:"relPath"`
	Title    string `json:"title"`
	Modified string `json:"modified"`
}

// backupStats summarizes a notebook for the status bar.
type backupStats struct {
	NoteCount  int
	FolderCount int
	ImageCount int
	TotalWords int
	TotalChars int
}

// collectBackupStats walks the store and produces summary statistics.
func (m *Model) collectBackupStats() backupStats {
	var stats backupStats
	for _, item := range m.flat {
		if item.node.IsDir {
			stats.FolderCount++
			continue
		}
		stats.NoteCount++
		content, err := m.store.Read(item.node.RelPath)
		if err != nil {
			continue
		}
		stats.TotalWords += wordCount(content)
		stats.TotalChars += charCount(content)
	}
	if m.images != nil {
		if dir, err := m.images.imagesDir(); err == nil {
			entries, _ := os.ReadDir(dir)
			stats.ImageCount = len(entries)
		}
	}
	return stats
}

// exportBackup writes a zip containing all notes and the manifest to dest.
func (m *Model) exportBackup(dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// Write manifest
	manifest := backupManifest{
		Version:   "1",
		CreatedAt: time.Now().Format(time.RFC3339),
		Tags:      m.nodeTags,
		Pinned:    m.nodePinned,
		Notes:     []backupNoteEntry{},
	}
	for _, item := range m.flat {
		if item.node.IsDir {
			continue
		}
		content, err := m.store.Read(item.node.RelPath)
		if err != nil {
			continue
		}
		absPath, err := m.store.Resolve(item.node.RelPath)
		if err != nil {
			continue
		}
		info, _ := os.Stat(absPath)
		modStr := ""
		if info != nil {
			modStr = info.ModTime().Format(time.RFC3339)
		}
		entry := backupNoteEntry{
			RelPath:  item.node.RelPath,
			Title:    strings.TrimSuffix(item.node.Name, filepath.Ext(item.node.Name)),
			Modified: modStr,
		}
		manifest.Notes = append(manifest.Notes, entry)

		// Write note file inside zip
		w, err := zw.Create(item.node.RelPath)
		if err != nil {
			continue
		}
		_, _ = w.Write([]byte(content))
	}

	// Write images
	if m.images != nil {
		if dir, err := m.images.imagesDir(); err == nil {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					continue
				}
				w, err := zw.Create(filepath.Join("images", e.Name()))
				if err != nil {
					continue
				}
				_, _ = w.Write(data)
			}
		}
	}

	// Write manifest JSON
	mw, err := zw.Create("tn-backup.json")
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize backup: %w", err)
	}
	return nil
}

// importBackup reads a zip and restores its contents into the store.
func (m *Model) importBackup(src string) (int, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return 0, fmt.Errorf("open backup: %w", err)
	}
	defer zr.Close()

	// Read manifest first (to get tags/pinned)
	var manifest backupManifest
	for _, f := range zr.File {
		if f.Name == "tn-backup.json" {
			rc, err := f.Open()
			if err != nil {
				return 0, err
			}
			_ = json.NewDecoder(rc).Decode(&manifest)
			rc.Close()
			break
		}
	}

	// Import notes and images
	count := 0
	for _, f := range zr.File {
		if f.Name == "tn-backup.json" {
			continue
		}
		if strings.HasPrefix(f.Name, "images/") {
			if m.images == nil {
				continue
			}
			dir, err := m.images.imagesDir()
			if err != nil {
				continue
			}
			data, err := readZipFile(f)
			if err != nil {
				continue
			}
			_ = os.WriteFile(filepath.Join(dir, strings.TrimPrefix(f.Name, "images/")), data, 0o644)
			continue
		}
		if !strings.EqualFold(filepath.Ext(f.Name), ".md") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			continue
		}
		abs, err := m.store.Resolve(f.Name)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			continue
		}
		count++
	}

	// Restore tags and pinned
	if manifest.Tags != nil {
		for k, v := range manifest.Tags {
			m.nodeTags[k] = v
		}
	}
	if manifest.Pinned != nil {
		for k, v := range manifest.Pinned {
			m.nodePinned[k] = v
		}
	}

	return count, nil
}

// importDir imports all .md files from a directory into the notebook root.
// Existing files are skipped.
func (m *Model) importDir(srcDir string) (int, int, error) {
	imported := 0
	skipped := 0
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		abs, err := m.store.Resolve(rel)
		if err != nil {
			return nil
		}
		if _, err := os.Stat(abs); err == nil {
			skipped++
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return nil
		}
		imported++
		return nil
	})
	return imported, skipped, err
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// sortBackupEntries sorts note entries by title for display.
func sortBackupEntries(entries []backupNoteEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Title) < strings.ToLower(entries[j].Title)
	})
}
