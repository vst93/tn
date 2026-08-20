package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// render is a test helper: markdown in, plain rendered lines out.
func mdLines(t *testing.T, src string, width int) []string {
	t.Helper()
	out, err := newMdRenderer(width).render(src)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(stripANSI(out), "\n")
}

func mdText(t *testing.T, src string, width int) string {
	t.Helper()
	return strings.Join(mdLines(t, src, width), "\n")
}

func TestMarkdownDropsFrontMatterAndHashes(t *testing.T) {
	got := mdText(t, "---\ntitle: Note\ntags: [a]\n---\n\n# Title\n\n## Section\n", 60)
	for _, unwanted := range []string{"title: Note", "tags:", "## ", "# "} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("expected %q to be gone, got:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Section") {
		t.Fatalf("headings lost:\n%s", got)
	}
	if !strings.Contains(got, "━━━") || !strings.Contains(got, "───") {
		t.Fatalf("expected H1 and H2 rules:\n%s", got)
	}
}

func TestMarkdownListsHangUnderTheirMarker(t *testing.T) {
	src := "- " + strings.Repeat("word ", 20) + "\n- short\n  - nested\n"
	lines := mdLines(t, src, 40)
	var first, cont string
	for i, line := range lines {
		if strings.Contains(line, "•") && first == "" {
			first = line
			if i+1 < len(lines) {
				cont = lines[i+1]
			}
		}
	}
	if first == "" {
		t.Fatalf("no bullet rendered:\n%s", strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(cont) == "" || strings.Contains(cont, "•") {
		t.Fatalf("expected a wrapped continuation line, got %q", cont)
	}
	if indent := len(cont) - len(strings.TrimLeft(cont, " ")); indent != 2 {
		t.Fatalf("expected continuation to hang under the text at column 2, got %d in %q", indent, cont)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "◦ nested") {
		t.Fatalf("expected a nested bullet glyph:\n%s", strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("line exceeds wrap width (%d): %q", w, line)
		}
	}
}

func TestMarkdownTasksAndDivider(t *testing.T) {
	got := mdText(t, "- [ ] open\n- [x] done\n\n---\n", 40)
	for _, want := range []string{"[ ] open", "[✓] done", "·  ·  ·"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in:\n%s", want, got)
		}
	}
}

func TestMarkdownCodeCardFramesEveryLine(t *testing.T) {
	src := "```go\nfunc main() {}\n" + strings.Repeat("x", 90) + "\n```\n"
	lines := mdLines(t, src, 50)
	var card []string
	for _, line := range lines {
		if strings.ContainsAny(line, "╭│╰") {
			card = append(card, line)
		}
	}
	if len(card) < 4 {
		t.Fatalf("expected a framed code card:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(card[0], "go") {
		t.Fatalf("expected the language in the top edge: %q", card[0])
	}
	width := lipgloss.Width(card[0])
	for _, line := range card {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("card line width %d != %d: %q", got, width, line)
		}
	}
	if width > 50 {
		t.Fatalf("card overflows the wrap width: %d", width)
	}
}

func TestMarkdownTableFitsAndAligns(t *testing.T) {
	src := "| Name | Value |\n|:-----|------:|\n| alpha | 1 |\n| beta | 22 |\n"
	lines := mdLines(t, src, 40)
	var rows []string
	for _, line := range lines {
		if strings.Contains(line, "│") || strings.Contains(line, "┼") {
			rows = append(rows, line)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("expected header, rule and two rows:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(rows[1], "┼") {
		t.Fatalf("expected a header rule, got %q", rows[1])
	}
	if !strings.Contains(rows[2], "alpha") || !strings.HasSuffix(strings.TrimRight(rows[2], " "), "1") {
		t.Fatalf("expected right-aligned value, got %q", rows[2])
	}
	for _, row := range rows {
		if w := lipgloss.Width(row); w > 40 {
			t.Fatalf("table row too wide (%d): %q", w, row)
		}
	}
}

func TestMarkdownWideTableShrinksAndWraps(t *testing.T) {
	src := "| a | b |\n|---|---|\n| " + strings.Repeat("long ", 12) + "| " +
		strings.Repeat("wide ", 12) + "|\n"
	for _, line := range mdLines(t, src, 40) {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("line %q is %d cells wide", line, w)
		}
	}
}

func TestMarkdownFootnotesBecomeNumbers(t *testing.T) {
	got := mdText(t, "text[^note] more\n\n[^note]: the note\n", 60)
	if !strings.Contains(got, "text[1] more") {
		t.Fatalf("expected a numbered reference:\n%s", got)
	}
	if !strings.Contains(got, "[1] the note") {
		t.Fatalf("expected a numbered definition:\n%s", got)
	}
}

func TestMarkdownLeavesNoMarkers(t *testing.T) {
	src := "# T\n\n## S\n\n---\n\n```\ncode\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"![shot](./images/x.png)\n"
	out, err := newMdRenderer(48).render(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, '\x01') {
		t.Fatalf("layout markers leaked into the preview: %q", out)
	}
	if !strings.Contains(stripANSI(out), "[📷 shot: x.png]") {
		t.Fatalf("expected an image placeholder:\n%s", stripANSI(out))
	}
}

func TestMarkdownFenceInsideListStaysWithTheItem(t *testing.T) {
	got := mdText(t, "- item\n\n  ```go\n  x := 1\n  ```\n", 50)
	if strings.Contains(got, "╭") {
		t.Fatalf("indented fences stay with glamour, got:\n%s", got)
	}
	if !strings.Contains(got, "x := 1") {
		t.Fatalf("indented fence lost its code:\n%s", got)
	}
}

func TestMarkdownUnclosedFenceStillRenders(t *testing.T) {
	got := mdText(t, "before\n\n```go\nx := 1\n", 40)
	if !strings.Contains(got, "before") || !strings.Contains(got, "x := 1") {
		t.Fatalf("unclosed fence dropped content:\n%s", got)
	}
}
