package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
)

func renderDoc(t *testing.T, doc string, width int) string {
	t.Helper()
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(decorateCodeBlocks(out, width))
}

func show(t *testing.T, doc string, width int) {
	t.Helper()
	out := renderDoc(t, doc, width)
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		fmt.Printf("%02d |%s| (w=%d)\n", i, stripANSI(ln), lipglossWidth(ln))
	}
}

func TestScratchRender(t *testing.T) {
	for _, w := range []int{60, 40} {
		fmt.Printf("\n########## WIDTH %d ##########\n", w)
		fmt.Println("--- blank line in code block ---")
		show(t, "```go\nfunc a() {\n\n\treturn 1\n}\n```", w)
		fmt.Println("--- empty code block ---")
		show(t, "before\n\n```\n```\n\nafter", w)
		fmt.Println("--- code with tab ---")
		show(t, "```go\n\t\tindented\n\tdeep\nnoindent\n```", w)
		fmt.Println("--- task list ---")
		show(t, "- [ ] open\n- [x] done\n  - [ ] nested task\n", w)
		fmt.Println("--- nested list multiline ---")
		show(t, "- parent one\n  - child a\n  - child b\n- parent two\n\n  continuation text\n\n- parent three", w)
		fmt.Println("--- table ---")
		show(t, "| Name | Value | Notes |\n|------|-------|-------|\n| alpha | 1 | aaa |\n| beta | 22 | bbbbb |", w)
		fmt.Println("--- table wide cell ---")
		show(t, "| a |\n|---|\n| "+strings.Repeat("x", 120)+" |", w)
	}
}
func lipglossWidth(s string) int {
	return len([]rune(stripANSI(s)))
}
