package app

// Markdown rendering for the preview pane.
//
// A note is rendered in three steps:
//
//  1. mdPrepare drops YAML front matter, turns image links into terminal-safe
//     placeholders and lifts the blocks TN draws itself (fenced code, GFM
//     tables) out of the source, leaving a marker behind.
//  2. goldmark + glamour's ANSI renderer style everything that is left.
//  3. line passes re-insert the extracted blocks, expand markers into rules
//     and re-flow list items with a hanging indent.
//
// Structural decoration goes through markers rather than glamour's style
// config because glamour renders a block suffix with the *parent* style — a
// heading rule could never get its own color — and because its table renderer
// always stretches a table to the full available width.

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/quick"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	glamansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	termansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// mdRenderer renders note markdown at a fixed wrap width.
type mdRenderer struct {
	md    goldmark.Markdown
	width int
}

// newMdRenderer builds the goldmark pipeline for one wrap width. glamour's
// own constructor is not used because TN needs its own style config and a
// pipeline it can extend.
func newMdRenderer(width int) *mdRenderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(
		util.Prioritized(glamansi.NewRenderer(glamansi.Options{
			WordWrap:        width,
			ColorProfile:    termenv.TrueColor,
			Styles:          markdownStyle(),
			ChromaFormatter: mdChromaFormatter,
		}), 1000),
	)))
	return &mdRenderer{md: md, width: width}
}

// render turns note source into styled ANSI text for the preview pane.
func (r *mdRenderer) render(src string) (string, error) {
	body, blocks := mdPrepare(src)
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(body), &buf); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	lines := strings.Split(buf.String(), "\n")
	for i, line := range lines {
		lines[i] = mdTrimRight(line)
	}
	lines = mdJoinGroups(lines)
	lines = mdReflowLists(lines, r.width)
	lines = mdSplice(lines, blocks, r.width)
	lines = mdCollapseBlanks(lines)
	out := mdMarkRe.ReplaceAllString(strings.Join(lines, "\n"), "")
	return strings.Trim(out, "\n"), nil
}

// Markers stand in for blocks and rules that TN draws itself. They start with
// a control character so any leftover is stripped from copied plain text.
const (
	mdMarkH1   = "\x01h1\x01"
	mdMarkH2   = "\x01h2\x01"
	mdMarkRule = "\x01hr\x01"
)

var mdMarkRe = regexp.MustCompile("\x01[a-z0-9]+\x01")

func mdMark(kind string, idx int) string { return "\x01" + kind + strconv.Itoa(idx) + "\x01" }

// mdBlocks collects the blocks lifted out of the source by mdPrepare.
type mdBlocks struct {
	code   []mdCode
	tables []mdTable
}

// mdCode is one fenced code block.
type mdCode struct {
	lang string
	body []string
}

// mdPrepare rewrites note source into something glamour renders well and
// returns the blocks TN draws itself. Only top-level fences and tables are
// lifted out: an indented fence belongs to a list item, where a marker would
// break the item apart, so those stay in the source for glamour to render.
func mdPrepare(src string) (string, mdBlocks) {
	var blocks mdBlocks
	var notes mdFootnoter
	_, body := parseFrontMatter(src)
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines)+8)
	for i := 0; i < len(lines); i++ {
		if lang, fence, ok := mdFenceStart(lines[i]); ok {
			code, next := mdCollectFence(lines, i+1, fence)
			blocks.code = append(blocks.code, mdCode{lang: lang, body: code})
			out = append(out, "", mdMark("c", len(blocks.code)-1), "")
			i = next
			continue
		}
		if table, next, ok := mdTableAt(lines, i); ok {
			blocks.tables = append(blocks.tables, table)
			out = append(out, "", mdMark("t", len(blocks.tables)-1), "")
			i = next
			continue
		}
		if placeholder, ok := imageLinePlaceholder(lines[i]); ok {
			out = append(out, placeholder)
			continue
		}
		out = append(out, notes.rewrite(lines[i]))
	}
	return strings.Join(mdInsertGroupMarks(out), "\n"), blocks
}

// mdGroupMark stands between two lines the author typed as consecutive rows.
// glamour merges soft line breaks inside a paragraph into one flowed block,
// so after rendering mdJoinGroups uses the marker to stitch those blocks back
// together without the blank line — the preview then mirrors the author's
// line breaks instead of reflowing prose into a wall of text.
var mdGroupMark = "\x01g\x01"

// mdInsertGroupMarks places the group mark between adjacent plain text lines.
// Anything that is its own block type (lists, quotes, headings, tables,
// fences, rules) is left alone: those follow standard Markdown flow.
func mdInsertGroupMarks(lines []string) []string {
	out := make([]string, 0, len(lines)+16)
	for i, line := range lines {
		out = append(out, line)
		if i+1 < len(lines) && mdPlainLine(line) && mdPlainLine(lines[i+1]) {
			out = append(out, "", mdGroupMark, "")
		}
	}
	return out
}

// srcListRe matches list items as they appear in note source (dash, star,
// plus, ordered and task markers), at any indent.
var srcListRe = regexp.MustCompile(`^ *([-*+] |\d+[.)] |\[[ x✓]\] )`)

// mdPlainLine reports whether a source line is ordinary paragraph text that
// participates in author line breaks.
func mdPlainLine(s string) bool {
	t := strings.TrimSpace(s)
	switch {
	case t == "", mdMarkRe.MatchString(t):
		return false
	case strings.HasPrefix(t, "#"), strings.HasPrefix(t, ">"), strings.HasPrefix(t, "|"):
		return false
	case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
		return false
	case srcListRe.MatchString(s), mdItemRe.MatchString(t):
		return false
	case strings.Trim(t, "=-") == "": // rules and setext underlines
		return false
	}
	return true
}

// mdJoinGroups stitches marked paragraphs together: the marker line and one
// neighboring blank disappear so consecutive typed lines render tightly.
func mdJoinGroups(lines []string) []string {
	out := make([]string, 0, len(lines))
	skipBlank := false
	for _, line := range lines {
		plain := strings.TrimSpace(stripANSI(line))
		if plain == mdGroupMark {
			if n := len(out); n > 0 && strings.TrimSpace(stripANSI(out[n-1])) == "" {
				out = out[:n-1]
			}
			skipBlank = true
			continue
		}
		if skipBlank && strings.TrimSpace(stripANSI(line)) == "" {
			skipBlank = false
			continue
		}
		skipBlank = false
		out = append(out, line)
	}
	return out
}

var (
	mdFootnoteRefRe = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
	mdFootnoteDefRe = regexp.MustCompile(`^(\s*\[\d+\]):\s*`)
)

// mdFootnoter numbers footnotes in order of appearance and rewrites them as
// plain "[n]" text. goldmark's footnote extension is deliberately not enabled:
// glamour has no renderer for footnote nodes and prints "unhandled element" on
// stdout, straight through the TUI.
type mdFootnoter struct{ ids map[string]int }

func (f *mdFootnoter) rewrite(line string) string {
	if !strings.Contains(line, "[^") {
		return line
	}
	if f.ids == nil {
		f.ids = make(map[string]int)
	}
	out := mdFootnoteRefRe.ReplaceAllStringFunc(line, func(match string) string {
		id := match[2 : len(match)-1]
		n, ok := f.ids[id]
		if !ok {
			n = len(f.ids) + 1
			f.ids[id] = n
		}
		return "[" + strconv.Itoa(n) + "]"
	})
	return mdFootnoteDefRe.ReplaceAllString(out, "$1 ")
}

// mdFenceStart reports whether the line opens a top-level code fence and
// returns the info string's language and the fence itself.
func mdFenceStart(line string) (string, string, bool) {
	trimmed := strings.TrimRight(line, " \t")
	for _, marker := range []string{"```", "~~~"} {
		if !strings.HasPrefix(trimmed, marker) {
			continue
		}
		n := 0
		for n < len(trimmed) && trimmed[n] == marker[0] {
			n++
		}
		info := strings.TrimSpace(trimmed[n:])
		if strings.ContainsRune(info, rune(marker[0])) {
			continue
		}
		lang, _, _ := strings.Cut(info, " ")
		return strings.ToLower(lang), trimmed[:n], true
	}
	return "", "", false
}

// mdCollectFence gathers code lines up to the closing fence and returns the
// index of that fence (or of the last line when the fence is never closed).
func mdCollectFence(lines []string, start int, fence string) ([]string, int) {
	var code []string
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimRight(lines[i], " \t")
		if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
			return code, i
		}
		code = append(code, lines[i])
	}
	return code, len(lines) - 1
}

var mdItemRe = regexp.MustCompile(`^( *)(• |\d+\. |\[[ x✓]\] )(.*)$`)

// mdReflowLists gives wrapped list items a hanging indent and styles their
// markers. glamour wraps item text back to the item's own indent, which reads
// as a new item, and it renders every marker in the document color because a
// list marker is emitted with the parent block's style.
func mdReflowLists(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		match := mdItemRe.FindStringSubmatch(stripANSI(lines[i]))
		if match == nil {
			out = append(out, lines[i])
			continue
		}
		indent, marker := len(match[1]), match[2]
		content := termansi.TruncateLeft(lines[i], indent+lipgloss.Width(marker), "")
		prev := stripANSI(lines[i])
		j := i + 1
		for ; j < len(lines); j++ {
			plain := stripANSI(lines[j])
			if strings.TrimSpace(plain) == "" || mdItemRe.MatchString(plain) {
				break
			}
			if mdMarkRe.MatchString(plain) {
				break
			}
			if len(plain)-len(strings.TrimLeft(plain, " ")) != indent {
				break
			}
			if !mdIsWrapped(prev, plain, mdContentWidth(width)) {
				break
			}
			content += " " + termansi.TruncateLeft(lines[j], indent, "")
			prev = plain
		}
		i = j - 1
		out = append(out, mdWrapItem(indent, marker, content, width)...)
		// Lines the author broke off themselves stay on their own row, hung
		// under the item text instead of sitting flush at column zero.
		_, cells := mdItemMarker(indent, marker)
		hang := strings.Repeat(" ", indent+cells)
		for ; j < len(lines); j++ {
			plain := stripANSI(lines[j])
			if strings.TrimSpace(plain) == "" || mdItemRe.MatchString(plain) {
				break
			}
			if mdMarkRe.MatchString(plain) {
				break
			}
			if len(plain)-len(strings.TrimLeft(plain, " ")) != indent {
				break
			}
			out = append(out, hang+termansi.TruncateLeft(lines[j], indent, ""))
		}
		i = j - 1
	}
	return out
}

// mdIsWrapped reports whether next can only be the continuation of a wrapped
// line. glamour wraps greedily, so if next's first word still fitted on prev it
// would have been put there: next must start a new block instead. Without the
// check, a paragraph that follows a list would be pulled into the last item.
func mdIsWrapped(prev, next string, limit int) bool {
	word, _, _ := strings.Cut(strings.TrimLeft(next, " "), " ")
	return lipgloss.Width(prev)+1+lipgloss.Width(word) > limit
}

// mdWrapItem lays out one list item: styled marker, then text wrapped to the
// remaining width with continuation lines aligned under the text.
func mdWrapItem(indent int, marker, content string, width int) []string {
	styled, cells := mdItemMarker(indent, marker)
	avail := max(8, mdContentWidth(width)-indent-cells)
	pad := strings.Repeat(" ", indent)
	hang := pad + strings.Repeat(" ", cells)
	var out []string
	for k, line := range strings.Split(termansi.Wrap(content, avail, ""), "\n") {
		if k == 0 {
			out = append(out, pad+styled+line)
			continue
		}
		out = append(out, hang+line)
	}
	return out
}

// mdItemMarker styles a list marker and reports how many cells it occupies.
// Bullet depth is carried by the glyph so nesting stays readable once the text
// is wrapped: every list level indents by two columns.
func mdItemMarker(indent int, marker string) (string, int) {
	switch {
	case strings.HasPrefix(marker, "["):
		if strings.ContainsAny(marker, "x✓") {
			return lipgloss.NewStyle().Foreground(green).Render("[✓]") + " ", 4
		}
		return lipgloss.NewStyle().Foreground(muted).Render("[ ]") + " ", 4
	case marker == "• ":
		glyphs := []string{"•", "◦", "▪"}
		level := (indent / 2) % len(glyphs)
		color := accent
		if level > 0 {
			color = muted
		}
		return lipgloss.NewStyle().Foreground(color).Render(glyphs[level]) + " ", 2
	default:
		return lipgloss.NewStyle().Foreground(accent).Render(strings.TrimSuffix(marker, " ")) + " ",
			lipgloss.Width(marker)
	}
}

// mdSplice replaces marker lines with the blocks and rules TN draws itself.
func mdSplice(lines []string, blocks mdBlocks, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		mark := strings.TrimSpace(stripANSI(line))
		switch {
		case mark == mdMarkH1:
			out = append(out, mdRuleLine("━", rule, width))
		case mark == mdMarkH2:
			out = append(out, mdRuleLine("─", rule, width))
		case mark == mdMarkRule:
			out = append(out, "", mdDivider(width), "")
		case strings.HasPrefix(mark, "\x01c"):
			if idx, ok := mdMarkIndex(mark, "c"); ok && idx < len(blocks.code) {
				out = append(out, mdRenderCode(blocks.code[idx], width)...)
				continue
			}
			out = append(out, line)
		case strings.HasPrefix(mark, "\x01t"):
			if idx, ok := mdMarkIndex(mark, "t"); ok && idx < len(blocks.tables) {
				out = append(out, mdRenderTable(blocks.tables[idx], width)...)
				continue
			}
			out = append(out, line)
		default:
			out = append(out, line)
		}
	}
	return out
}

func mdMarkIndex(mark, kind string) (int, bool) {
	digits := strings.TrimSuffix(strings.TrimPrefix(mark, "\x01"+kind), "\x01")
	idx, err := strconv.Atoi(digits)
	if err != nil || idx < 0 {
		return 0, false
	}
	return idx, true
}

// mdContentWidth is the usable text width for blocks TN lays out itself.
func mdContentWidth(width int) int { return max(4, width) }

// mdRuleLine draws a full-width rule under a heading.
func mdRuleLine(glyph string, color lipgloss.TerminalColor, width int) string {
	return lipgloss.NewStyle().Foreground(color).
		Render(strings.Repeat(glyph, mdContentWidth(width)))
}

// mdDivider renders a thematic break as centered dots: a full-width line would
// be indistinguishable from a heading rule.
func mdDivider(width int) string {
	inner := mdContentWidth(width)
	dots := "·  ·  ·"
	pad := max(0, (inner-lipgloss.Width(dots))/2)
	return strings.Repeat(" ", pad) + lipgloss.NewStyle().Foreground(muted).Render(dots)
}

// mdTrimRight drops the trailing padding glamour adds to every line while
// keeping the line's ANSI sequences intact.
func mdTrimRight(line string) string {
	plain := stripANSI(line)
	trimmed := strings.TrimRight(plain, " \t")
	if len(trimmed) == len(plain) {
		return line
	}
	if trimmed == "" {
		return ""
	}
	return termansi.Truncate(line, lipgloss.Width(trimmed), "")
}

// mdCollapseBlanks reduces runs of blank lines to a single one. glamour emits a
// blank line per block boundary, which stacks up around lists and markers.
func mdCollapseBlanks(lines []string) []string {
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(stripANSI(line)) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return out
}

// mdRenderCode draws a fenced code block as a framed card: a thin rule-colored
// border with the language set into its top edge, and a chroma-highlighted body
// on the surface background.
func mdRenderCode(block mdCode, width int) []string {
	box := max(16, mdContentWidth(width))
	inner := box - 4
	border := lipgloss.NewStyle().Foreground(rule)
	label := ""
	if block.lang != "" {
		label = " " + block.lang + " "
	}
	fill := max(0, box-3-lipgloss.Width(label))
	out := []string{
		border.Render("╭─") + mutedSty.Render(label) +
			border.Render(strings.Repeat("─", fill)+"╮"),
	}
	code := strings.ReplaceAll(strings.Join(block.body, "\n"), "\t", "    ")
	body := strings.Split(strings.TrimRight(mdHighlight(code, block.lang), "\n"), "\n")
	bg := termansi.Style{}.BackgroundColor(termansi.TrueColor(surfaceRGB)).String()
	edge := border.Render("│")
	for _, line := range body {
		for _, part := range strings.Split(termansi.Hardwrap(line, inner, true), "\n") {
			gap := max(0, inner-lipgloss.Width(part))
			out = append(out, edge+bg+" "+mdCodeBg(part, bg)+
				strings.Repeat(" ", gap)+" "+termansi.ResetStyle+edge)
		}
	}
	return append(out, border.Render("╰"+strings.Repeat("─", box-2)+"╯"))
}

// mdCodeBg re-applies the code background after every reset. Chroma resets its
// style after each token, which would otherwise punch holes in the card.
func mdCodeBg(line, bg string) string {
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg)
	if termansi.ResetStyle != "\x1b[0m" {
		line = strings.ReplaceAll(line, termansi.ResetStyle, termansi.ResetStyle+bg)
	}
	return line
}

// mdHighlight runs chroma over one code block, falling back to plain text for
// unknown languages or a broken theme.
func mdHighlight(code, lang string) string {
	theme := mdChromaTheme()
	if theme == "" {
		return code
	}
	var buf bytes.Buffer
	if err := quick.Highlight(&buf, code, lang, mdChromaFormatter, theme); err != nil {
		return code
	}
	return buf.String()
}

// mdAlign is a table column alignment.
type mdAlign int

const (
	mdAlignLeft mdAlign = iota
	mdAlignCenter
	mdAlignRight
)

// mdTable is a parsed GFM table.
type mdTable struct {
	head  []string
	rows  [][]string
	align []mdAlign
}

var mdDelimRe = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

// mdTableAt parses a GFM table starting at lines[i] and returns the index of
// its last line.
func mdTableAt(lines []string, i int) (mdTable, int, bool) {
	var table mdTable
	if i+1 >= len(lines) || !mdIsTableRow(lines[i]) || !mdIsTableDelim(lines[i+1]) {
		return table, i, false
	}
	table.head = mdSplitRow(lines[i])
	delim := mdSplitRow(lines[i+1])
	if len(table.head) < 2 || len(delim) != len(table.head) {
		return table, i, false
	}
	for _, cell := range delim {
		table.align = append(table.align, mdAlignOf(cell))
	}
	next := i + 1
	for j := i + 2; j < len(lines); j++ {
		if !mdIsTableRow(lines[j]) {
			break
		}
		row := mdSplitRow(lines[j])
		for len(row) < len(table.head) {
			row = append(row, "")
		}
		table.rows = append(table.rows, row[:len(table.head)])
		next = j
	}
	return table, next, true
}

func mdIsTableRow(line string) bool {
	trimmed := strings.TrimRight(line, " \t")
	return trimmed != "" && !strings.HasPrefix(trimmed, " ") && strings.Contains(trimmed, "|")
}

func mdIsTableDelim(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "-") && mdDelimRe.MatchString(trimmed)
}

func mdAlignOf(cell string) mdAlign {
	cell = strings.TrimSpace(cell)
	left, right := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
	switch {
	case left && right:
		return mdAlignCenter
	case right:
		return mdAlignRight
	}
	return mdAlignLeft
}

// mdSplitRow splits a table row into trimmed cells, honouring escaped pipes.
func mdSplitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "|"), "|")
	var cells []string
	var cur strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			if r != '|' {
				cur.WriteRune('\\')
			}
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// mdRenderTable draws a table with natural column widths, an accented header
// and hairline separators. glamour's table renderer always fills the available
// width and cannot style the header row, which is what made tables look boxy.
func mdRenderTable(table mdTable, width int) []string {
	cols := len(table.head)
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for c := range widths {
		widths[c] = lipgloss.Width(mdInlinePlain(table.head[c]))
		for _, row := range table.rows {
			widths[c] = max(widths[c], lipgloss.Width(mdInlinePlain(row[c])))
		}
		widths[c] = max(1, widths[c])
	}
	avail := mdContentWidth(width)
	for mdTableWidth(widths) > avail {
		widest := 0
		for c := range widths {
			if widths[c] > widths[widest] {
				widest = c
			}
		}
		if widths[widest] <= 3 {
			break
		}
		widths[widest]--
	}
	sep := lipgloss.NewStyle().Foreground(rule).Render("│")
	head := lipgloss.NewStyle().Foreground(accent).Bold(true)
	out := mdTableRow(table.head, widths, table.align, sep, head)
	segments := make([]string, cols)
	for c, w := range widths {
		segments[c] = strings.Repeat("─", w+2)
	}
	out = append(out, lipgloss.NewStyle().Foreground(rule).Render(strings.Join(segments, "┼")))
	body := lipgloss.NewStyle().Foreground(text)
	for _, row := range table.rows {
		out = append(out, mdTableRow(row, widths, table.align, sep, body)...)
	}
	return out
}

func mdTableWidth(widths []int) int {
	total := len(widths) - 1
	for _, w := range widths {
		total += w + 2
	}
	return total
}

// mdTableRow renders one row, wrapping cells that do not fit and padding each
// column according to its alignment.
func mdTableRow(cells []string, widths []int, align []mdAlign, sep string, base lipgloss.Style) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for c := range widths {
		cell := ""
		if c < len(cells) {
			cell = mdInline(cells[c], base)
		}
		wrapped[c] = strings.Split(termansi.Wrap(cell, widths[c], ""), "\n")
		height = max(height, len(wrapped[c]))
	}
	out := make([]string, 0, height)
	for r := 0; r < height; r++ {
		var b strings.Builder
		for c := range widths {
			if c > 0 {
				b.WriteString(sep)
			}
			cell := ""
			if r < len(wrapped[c]) {
				cell = wrapped[c][r]
			}
			b.WriteString(" " + mdPad(cell, widths[c], align[c]) + " ")
		}
		out = append(out, b.String())
	}
	return out
}

func mdPad(s string, width int, align mdAlign) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return termansi.Truncate(s, width, "…")
	}
	switch align {
	case mdAlignRight:
		return strings.Repeat(" ", gap) + s
	case mdAlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", gap-left)
	}
	return s + strings.Repeat(" ", gap)
}

// Inline markdown inside table cells is rendered by TN, because cells never
// reach glamour. The subset below is what shows up in practice. Emphasis is
// asterisk-only: `_` is a word character, so matching it would italicize the
// middle of every snake_case identifier in a cell.
var mdInlineRes = []*regexp.Regexp{
	regexp.MustCompile("`+[^`]+`+"),               // code span
	regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`),   // link
	regexp.MustCompile(`\*\*[^*]+\*\*|__[^_]+__`), // strong
	regexp.MustCompile(`~~[^~]+~~`),               // strikethrough
	regexp.MustCompile(`\*[^*\s][^*]*\*`),         // emphasis
}

// mdInline styles inline markdown, leaving plain runs in the base style.
func mdInline(s string, base lipgloss.Style) string {
	var b strings.Builder
	for _, span := range mdInlineSpans(s, base) {
		b.WriteString(span.style.Render(span.text))
	}
	return b.String()
}

// mdInlinePlain strips inline markup, for measuring a cell's width.
func mdInlinePlain(s string) string {
	var b strings.Builder
	for _, span := range mdInlineSpans(s, lipgloss.NewStyle()) {
		b.WriteString(span.text)
	}
	return b.String()
}

type mdSpan struct {
	text  string
	style lipgloss.Style
}

func mdInlineSpans(s string, base lipgloss.Style) []mdSpan {
	var spans []mdSpan
	for s != "" {
		kind, loc := -1, []int(nil)
		for k, re := range mdInlineRes {
			m := re.FindStringIndex(s)
			if m != nil && (loc == nil || m[0] < loc[0]) {
				kind, loc = k, m
			}
		}
		if loc == nil {
			return append(spans, mdSpan{s, base})
		}
		if loc[0] > 0 {
			spans = append(spans, mdSpan{s[:loc[0]], base})
		}
		spans = append(spans, mdInlineSpan(kind, s[loc[0]:loc[1]], base))
		s = s[loc[1]:]
	}
	return spans
}

func mdInlineSpan(kind int, match string, base lipgloss.Style) mdSpan {
	switch kind {
	case 0:
		return mdSpan{strings.Trim(match, "`"), lipgloss.NewStyle().Foreground(text).Background(surface)}
	case 1:
		label := mdInlineRes[1].ReplaceAllString(match, "$1")
		return mdSpan{label, lipgloss.NewStyle().Foreground(accent)}
	case 2:
		return mdSpan{strings.Trim(match, "*_"), base.Bold(true)}
	case 3:
		return mdSpan{strings.Trim(match, "~"), base.Strikethrough(true).Foreground(muted)}
	default:
		return mdSpan{strings.Trim(match, "*_"), base.Italic(true)}
	}
}

// markdownStyle is the glamour style config for the current theme. Rules and
// list markers are emitted as markers and finished by the line passes above.
func markdownStyle() glamansi.StyleConfig {
	return glamansi.StyleConfig{
		// No document margin: the preview panel already pads its content, and
		// a second gutter would push every rule and card off-center.
		Document: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{Color: stringPtr(textColor)},
		},
		BlockQuote: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color:  stringPtr(mutedColor),
				Italic: boolPtr(true),
			},
			Indent:      uintPtr(1),
			IndentToken: stringPtr(lipgloss.NewStyle().Foreground(rule).Render("▌") + " "),
		},
		List: glamansi.StyleList{LevelIndent: 2},
		Heading: glamansi.StyleBlock{
			StylePrimitive: glamansi.StylePrimitive{
				Color: stringPtr(accentColor), Bold: boolPtr(true), BlockSuffix: "\n",
			},
		},
		H1: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(accentColor), Bold: boolPtr(true),
			BlockSuffix: "\n" + mdMarkH1 + "\n",
		}},
		H2: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(accentColor), Bold: boolPtr(true),
			BlockSuffix: "\n" + mdMarkH2 + "\n",
		}},
		H3: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(accentColor), Bold: boolPtr(true),
		}},
		H4: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(textColor), Bold: boolPtr(true),
		}},
		H5: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(mutedColor), Bold: boolPtr(true),
		}},
		H6: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(mutedColor), Bold: boolPtr(true), Italic: boolPtr(true),
		}},
		Strong:         glamansi.StylePrimitive{Bold: boolPtr(true)},
		Emph:           glamansi.StylePrimitive{Italic: boolPtr(true)},
		Strikethrough:  glamansi.StylePrimitive{CrossedOut: boolPtr(true), Color: stringPtr(mutedColor)},
		HorizontalRule: glamansi.StylePrimitive{Format: mdMarkRule},
		Item:           glamansi.StylePrimitive{BlockPrefix: "• "},
		Enumeration:    glamansi.StylePrimitive{BlockPrefix: ". "},
		Task:           glamansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:           glamansi.StylePrimitive{Color: stringPtr(accentColor), Faint: boolPtr(true)},
		LinkText:       glamansi.StylePrimitive{Color: stringPtr(accentColor)},
		Image:          glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
		ImageText:      glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
		Code: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
			Color: stringPtr(textColor), BackgroundColor: stringPtr(surfaceColor),
		}},
		CodeBlock: glamansi.StyleCodeBlock{
			StyleBlock: glamansi.StyleBlock{
				StylePrimitive: glamansi.StylePrimitive{Color: stringPtr(mutedColor)},
				Margin:         uintPtr(2),
			},
			Theme: mdChromaTheme(),
		},
		DefinitionList: glamansi.StyleBlock{Indent: uintPtr(1)},
		DefinitionTerm: glamansi.StylePrimitive{Color: stringPtr(textColor), Bold: boolPtr(true)},
		DefinitionDescription: glamansi.StylePrimitive{
			Color: stringPtr(mutedColor), BlockPrefix: "\n", Prefix: "  ",
		},
	}
}

// mdChromaFormatter renders syntax colors as true color, matching the rest of
// the UI, which paints its own backgrounds with 24-bit sequences.
const mdChromaFormatter = "terminal16m"

// mdChromaTheme registers a chroma style for the active TN theme and returns
// its name. Chroma's registry is global and keyed by name, so each theme needs
// its own entry — reusing one name would freeze the colors of the first theme
// that happened to render a code block.
func mdChromaTheme() string {
	name := "tn-" + strings.ToLower(currentTheme().Name)
	if _, ok := chromastyles.Registry[name]; ok {
		return name
	}
	style, err := chroma.NewStyle(name, chroma.StyleEntries{
		chroma.Background:          textColor + " bg:" + surfaceColor,
		chroma.Text:                textColor,
		chroma.Error:               dangerColor,
		chroma.Comment:             mutedColor + " italic",
		chroma.CommentPreproc:      warningColor,
		chroma.Keyword:             accentColor,
		chroma.KeywordReserved:     accentColor,
		chroma.KeywordNamespace:    accentColor,
		chroma.KeywordType:         accentColor,
		chroma.KeywordConstant:     warningColor,
		chroma.Operator:            mutedColor,
		chroma.Punctuation:         mutedColor,
		chroma.Name:                textColor,
		chroma.NameBuiltin:         accentColor,
		chroma.NameTag:             accentColor,
		chroma.NameAttribute:       greenColor,
		chroma.NameClass:           textColor + " bold",
		chroma.NameConstant:        warningColor,
		chroma.NameDecorator:       warningColor,
		chroma.NameException:       dangerColor,
		chroma.NameFunction:        textColor + " bold",
		chroma.Literal:             textColor,
		chroma.LiteralNumber:       warningColor,
		chroma.LiteralDate:         warningColor,
		chroma.LiteralString:       greenColor,
		chroma.LiteralStringEscape: warningColor,
		chroma.GenericDeleted:      dangerColor,
		chroma.GenericInserted:     greenColor,
		chroma.GenericEmph:         "italic",
		chroma.GenericStrong:       "bold",
		chroma.GenericSubheading:   mutedColor,
	})
	if err != nil {
		return ""
	}
	chromastyles.Register(style)
	return name
}
