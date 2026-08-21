package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vst93/tn/internal/storage"
)

func TestZZDump(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	store := storage.New(t.TempDir())
	os.WriteFile(filepath.Join(store.Root, "a.md"), []byte("# Alpha\n\nhello\n"), 0o644)
	m := New(store)
	m.resize(120, 40)
	m.selectPath("a.md")
	m.openSelectedNote()
	v := m.View()
	re := regexp.MustCompile("\x1b\\[[0-9;]*m")
	plain := re.ReplaceAllString(v, "")
	lines := strings.Split(plain, "\n")
	fmt.Printf("total view lines=%d width=%d height=%d\n", len(lines), lipgloss.Width(lines[0]), 40)
	for i, l := range lines[:min(8, len(lines))] {
		fmt.Printf("%02d %d| %s\n", i, lipgloss.Width(l), l)
	}
	fmt.Printf("...last line: %q\n", lines[len(lines)-1])
}
