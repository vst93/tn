package app

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// imageStore manages local image files referenced by notes.
type imageStore struct {
	root string
}

// newImageStore creates an image store rooted at {notebookRoot}/images.
func newImageStore(notebookRoot string) *imageStore {
	return &imageStore{root: filepath.Join(notebookRoot, "images")}
}

// imagesDir returns the absolute path to the images directory, creating it if needed.
func (s *imageStore) imagesDir() (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("create images dir: %w", err)
	}
	return s.root, nil
}

// saveImage decodes raw image bytes and writes it to the images directory.
// Returns the relative path (e.g. "./images/img-20260816-120000.png") for markdown reference.
func (s *imageStore) saveImage(data []byte) (string, error) {
	dir, err := s.imagesDir()
	if err != nil {
		return "", err
	}
	ext := sniffExt(data)
	ts := time.Now().Format("20060102-150405.999")
	filename := fmt.Sprintf("img-%s.%s", ts, ext)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write image: %w", err)
	}
	return "./images/" + filename, nil
}

// sniffExt detects the image format from magic bytes.
func sniffExt(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}):
		return "png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg"
	case bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}):
		return "gif"
	case bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}):
		return "gif"
	case bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}):
		return "webp"
	default:
		return "png"
	}
}

// readImageFromClipboard attempts to read an image from the system clipboard.
// Returns raw image bytes, detected extension, and error (nil if no image).
func readImageFromClipboard() ([]byte, string, error) {
	switch runtime.GOOS {
	case "linux":
		// Try Wayland first, then X11.
		if data, err := exec.Command("wl-paste", "--type", "image/png").Output(); err == nil {
			return data, "png", nil
		}
		if data, err := exec.Command("wl-paste", "--type", "image/jpeg").Output(); err == nil {
			return data, "jpg", nil
		}
		if data, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output(); err == nil {
			return data, "png", nil
		}
		if data, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/jpeg", "-o").Output(); err == nil {
			return data, "jpg", nil
		}
	case "darwin":
		// macOS: would need a script to read clipboard image.
	case "windows":
		// PowerShell approach would go here.
	}
	return nil, "", fmt.Errorf("no image in clipboard or unsupported platform")
}

// extractImageRefs parses markdown content and returns all referenced image paths.
// Returns just the base filenames.
func extractImageRefs(content string) []string {
	var refs []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "![") {
			continue
		}
		start := strings.Index(trimmed, "]( ")
		if start < 0 {
			start = strings.Index(trimmed, "](")
		}
		if start < 0 {
			continue
		}
		rest := trimmed[start+2:]
		end := strings.Index(rest, ")")
		if end < 0 {
			continue
		}
		ref := rest[:end]
		if strings.HasPrefix(ref, "./images/") {
			refs = append(refs, filepath.Base(ref))
		}
	}
	return refs
}

// cleanupOrphanedImages removes image files not referenced by any note content.
// Returns the number of files removed.
func (s *imageStore) cleanupOrphanedImages(noteContents []string) (int, error) {
	dir, err := s.imagesDir()
	if err != nil {
		return 0, err
	}
	referenced := make(map[string]bool)
	for _, content := range noteContents {
		for _, base := range extractImageRefs(content) {
			referenced[base] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !referenced[entry.Name()] {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// imagePlaceholder returns a terminal-safe placeholder for an image reference.
func imagePlaceholder(alt, path string) string {
	base := filepath.Base(path)
	if alt != "" {
		return fmt.Sprintf("[📷 %s: %s]", alt, base)
	}
	return fmt.Sprintf("[📷 %s]", base)
}

// renderImagesInMarkdown replaces image syntax with terminal-safe placeholders
// for display in the preview pane. Only processes lines that are markdown images.
func renderImagesInMarkdown(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "![") {
			start := strings.Index(trimmed, "]( ")
			if start < 0 {
				start = strings.Index(trimmed, "](")
			}
			if start >= 0 {
				alt := trimmed[2:start]
				rest := trimmed[start+2:]
				end := strings.Index(rest, ")")
				if end >= 0 {
					path := rest[:end]
					b.WriteString(imagePlaceholder(alt, path))
					b.WriteString("\n")
					continue
				}
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// decodeImage decodes image bytes for validation.
func decodeImage(data []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	return img, format, nil
}
