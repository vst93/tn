package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
)

func TestScratchCode(t *testing.T) {
	width := 60
	r, _ := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle()), glamour.WithWordWrap(width))
	doc := "```go\nfunc a() {\n\n\treturn 1\n}\n```\n\ntext after"
	out, _ := r.Render(doc)
	out = strings.Trim(decorateCodeBlocks(out, width), "\n")
	for j, ln := range strings.Split(out, "\n") {
		p := stripANSI(ln)
		fmt.Printf("%02d |%s| (%d)\n", j, p, len([]rune(p)))
	}
}