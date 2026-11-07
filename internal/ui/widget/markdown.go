package widget

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

const (
	bulletMarker = "• "
	quotePrefix  = "│ "
	ruleRune     = "─"
	// codeTabWidth is the tab stop code blocks are expanded with: a raw tab
	// has no well-defined width once drawn cell by cell.
	codeTabWidth = 4
)

var (
	// mdParser parses GitHub Flavored Markdown (tables, strikethrough,
	// linkified URLs, task lists). Only the parser is used — rendering is
	// done by mdRenderer into Spans, never through goldmark's HTML renderer.
	// goldmark parsers hold no per-parse state, so one shared instance is
	// safe to use from any goroutine.
	mdParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

	// codeHighlighter colours fenced code blocks. A package-level instance
	// with default limits, rather than the store's highlight pool, because
	// RenderMarkdown is a pure function called from Block.Build closures that
	// have no access to the store; highlight.Highlighter is documented safe
	// for concurrent use, so sharing one costs nothing.
	codeHighlighter = highlight.New(highlight.Options{})

	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?(?:-->|\z)`)
	htmlTagRe     = regexp.MustCompile(`(?s)</?[A-Za-z][^>]*>`)
	htmlImgRe     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	htmlAltRe     = regexp.MustCompile(`(?is)\balt\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	// htmlRawTextRe matches the opening of a CommonMark type-1 HTML block.
	htmlRawTextRe = regexp.MustCompile(`(?i)^\s*<(script|style|pre|textarea)\b`)
)

// RenderMarkdown renders body, GitHub Flavored Markdown, as styled Span
// lines no wider than width display columns (width < 1 is treated as 1; a
// double-width character cannot fit in a single column, so callers should
// pass a realistic width). Text is word-wrapped; code is chopped to width
// instead (never reflowed) and syntax-highlighted by its fence's language.
// Unlike CommonMark, a single newline in a paragraph is a line break, as in
// GitHub's own PR and comment bodies. HTML comments are hidden and other
// HTML is reduced to its text. Only foreground colours and attributes are
// used, so a row's cursor background shows through.
//
// A blank body, or one with nothing visible (only an HTML comment, only
// fence markers), yields no lines: callers pick their own placeholder.
func RenderMarkdown(body string, width int) [][]Span {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	if width < 1 {
		width = 1
	}
	src := []byte(body)
	doc := mdParser.Parse(text.NewReader(src))
	return (&mdRenderer{src: src}).blocks(doc, width, false)
}

// mdRenderer walks a goldmark AST over src into Span lines.
type mdRenderer struct {
	src []byte
}

// blocks renders parent's block children, one blank line between
// consecutive non-empty blocks unless tight (the children of a tight list
// item).
func (r *mdRenderer) blocks(parent ast.Node, width int, tight bool) [][]Span {
	var out [][]Span
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		lines := r.block(c, width)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 && !tight {
			out = append(out, blankLine())
		}
		out = append(out, lines...)
	}
	return out
}

func (r *mdRenderer) block(n ast.Node, width int) [][]Span {
	switch n := n.(type) {
	case *ast.Heading:
		return r.paragraph(n, theme.Header, width)
	case *ast.Paragraph, *ast.TextBlock:
		return r.paragraph(n, theme.Base, width)
	case *ast.List:
		return r.list(n, width)
	case *ast.Blockquote:
		return r.quote(n, width)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return r.code(n, width)
	case *ast.ThematicBreak:
		return [][]Span{{{Text: strings.Repeat(ruleRune, width), Style: theme.Muted}}}
	case *ast.HTMLBlock:
		return r.htmlBlock(n, width)
	case *extast.Table:
		return r.table(n, width)
	default:
		return r.blocks(n, width, false)
	}
}

// paragraph word-wraps n's inline content, or returns nil when it has no
// visible text (for example a paragraph of only an HTML comment).
func (r *mdRenderer) paragraph(n ast.Node, base tcell.Style, width int) [][]Span {
	spans := r.inline(n, base)
	if strings.TrimSpace(spansText(spans)) == "" {
		return nil
	}
	lines := WrapSpans(spans, width)
	for i, l := range lines {
		lines[i] = trimTrailingSpace(l)
	}
	// A trailing line break (<br>) leaves empty lines that would only add
	// space before the next block.
	for len(lines) > 0 && SpanWidth(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// list renders l with a hanging indent: each item's body is rendered
// narrower by the marker width, then prefixed by the marker on its first
// line and by spaces on the rest, so wrapped text and nested lists line up
// with the item text. In an ordered list every marker, task checkboxes
// included, is padded to the widest one so all items share a text column.
func (r *mdRenderer) list(l *ast.List, width int) [][]Span {
	var markers [][]Span
	number := l.Start
	numberWidth := 0
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		m := r.marker(l, item, number)
		number++
		markers = append(markers, m)
		numberWidth = max(numberWidth, SpanWidth(m))
	}

	var out [][]Span
	i := 0
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := markers[i]
		i++
		if l.IsOrdered() {
			if pad := numberWidth - SpanWidth(marker); pad > 0 {
				marker = append(marker, Span{Text: strings.Repeat(" ", pad), Style: theme.Base})
			}
		}
		markerWidth := SpanWidth(marker)
		indent := Span{Text: strings.Repeat(" ", markerWidth), Style: theme.Base}

		if len(out) > 0 && !l.IsTight {
			out = append(out, blankLine())
		}
		body := r.blocks(item, max(1, width-markerWidth), l.IsTight)
		if len(body) == 0 {
			out = append(out, trimTrailingSpace(marker))
			continue
		}
		for j, line := range body {
			switch {
			case j == 0:
				out = append(out, append(append([]Span(nil), marker...), line...))
			case SpanWidth(line) == 0:
				out = append(out, line)
			default:
				out = append(out, append([]Span{indent}, line...))
			}
		}
	}
	return out
}

// marker returns item's list marker: "[ ] "/"[x] " for a task list item
// (instead of a bullet, which GitHub also omits for checkboxes), "N. " for
// an ordered list, else a bullet.
func (r *mdRenderer) marker(l *ast.List, item ast.Node, number int) []Span {
	if checked, ok := taskState(item); ok {
		if checked {
			return []Span{{Text: "[x]", Style: theme.Success}, {Text: " ", Style: theme.Base}}
		}
		return []Span{{Text: "[ ] ", Style: theme.Base}}
	}
	if l.IsOrdered() {
		return []Span{{Text: fmt.Sprintf("%d. ", number), Style: theme.Base}}
	}
	return []Span{{Text: bulletMarker, Style: theme.Base}}
}

// taskState reports whether item starts with a GFM task checkbox and, if
// so, whether it is checked.
func taskState(item ast.Node) (checked, ok bool) {
	first := item.FirstChild()
	if first == nil || first.FirstChild() == nil {
		return false, false
	}
	box, isBox := first.FirstChild().(*extast.TaskCheckBox)
	if !isBox {
		return false, false
	}
	return box.IsChecked, true
}

func (r *mdRenderer) quote(q *ast.Blockquote, width int) [][]Span {
	body := r.blocks(q, max(1, width-SpanWidth([]Span{{Text: quotePrefix}})), false)
	out := make([][]Span, len(body))
	for i, line := range body {
		out[i] = append([]Span{{Text: quotePrefix, Style: theme.Muted}}, line...)
	}
	return out
}

// code renders a fenced or indented code block: tabs expanded, trailing
// blank lines dropped, highlighted by the fence's language when chroma
// knows it (else muted), and chopped to width.
func (r *mdRenderer) code(n ast.Node, width int) [][]Span {
	segments := n.Lines()
	lines := make([]string, 0, segments.Len())
	for i := range segments.Len() {
		seg := segments.At(i)
		// Segment.Value already prepends the segment's Padding (the spaces
		// left over from a partly consumed tab), so it must not be added again.
		lines = append(lines, strings.TrimRight(string(seg.Value(r.src)), "\r\n"))
	}

	var lang string
	if fenced, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(fenced.Language(r.src))
	}
	return codeLines(lines, lang, width)
}

// codeLines renders source lines as a code block: tabs expanded, leading
// and trailing blank lines dropped, highlighted when lang names a chroma
// lexer (else muted), and chopped to width.
func codeLines(lines []string, lang string, width int) [][]Span {
	lines = slices.Clone(lines)
	for i, l := range lines {
		lines[i] = diff.ExpandTabs(l, codeTabWidth)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return nil
	}

	// A highlight error is treated like "no lexer": the code is still shown.
	tokens, err := codeHighlighter.LinesForLanguage(lang, lines)
	if err != nil {
		tokens = nil
	}

	var out [][]Span
	for i, line := range lines {
		spans := []Span{{Text: line, Style: theme.Muted}}
		if tokens != nil {
			spans = spans[:0]
			for _, tok := range tokens[i] {
				spans = append(spans, Span{Text: tok.Text, Style: theme.TokenStyle(tok.Type)})
			}
		}
		out = append(out, chopSpans(spans, width)...)
	}
	return out
}

// htmlBlock hides HTML comments and strips tags from an HTML block, keeping
// its text one source line per output line (empty lines dropped), so for
// example "<details><summary>Title</summary>" shows "Title". Raw-text
// blocks are special: <script> and <style> content is hidden (it is never
// shown by a browser), and <pre>/<textarea> content keeps its lines
// verbatim, shown like a code block.
func (r *mdRenderer) htmlBlock(n *ast.HTMLBlock, width int) [][]Span {
	var b strings.Builder
	segments := n.Lines()
	for i := range segments.Len() {
		seg := segments.At(i)
		b.Write(seg.Value(r.src))
	}
	if n.HasClosure() {
		b.Write(n.ClosureLine.Value(r.src))
	}
	content := htmlCommentRe.ReplaceAllString(b.String(), "")

	if m := htmlRawTextRe.FindStringSubmatch(content); m != nil {
		if tag := strings.ToLower(m[1]); tag == "script" || tag == "style" {
			return nil
		}
		var lines []string
		for line := range strings.SplitSeq(content, "\n") {
			line = htmlTagRe.ReplaceAllString(strings.TrimRight(line, "\r"), "")
			lines = append(lines, resolveEntities([]byte(line)))
		}
		return codeLines(lines, "", width)
	}

	var out [][]Span
	for line := range strings.SplitSeq(content, "\n") {
		if spans := htmlLineSpans(strings.TrimSpace(line)); len(spans) > 0 {
			out = append(out, WrapSpans(spans, width)...)
		}
	}
	return out
}

// htmlLineSpans reduces one line of an HTML block to its text, with every
// <img> shown as an image placeholder rather than dropped like other tags,
// so an image pasted into a PR body (GitHub inserts uploads as <img>) still
// leaves a trace. It returns nil for a line with nothing visible.
func htmlLineSpans(line string) []Span {
	var spans []Span
	text := func(s string) {
		if t := resolveEntities([]byte(htmlTagRe.ReplaceAllString(s, ""))); t != "" {
			spans = append(spans, Span{Text: t, Style: theme.Base})
		}
	}
	last := 0
	for _, m := range htmlImgRe.FindAllStringIndex(line, -1) {
		text(line[last:m[0]])
		spans = append(spans, Span{Text: imagePlaceholder(htmlImgAlt(line[m[0]:m[1]])), Style: theme.Muted})
		last = m[1]
	}
	text(line[last:])
	if strings.TrimSpace(spansText(spans)) == "" {
		return nil
	}
	spans[0].Text = strings.TrimLeft(spans[0].Text, " \t")
	spans[len(spans)-1].Text = strings.TrimRight(spans[len(spans)-1].Text, " \t")
	return spans
}

// htmlImgAlt returns the (entity-decoded) alt attribute of an <img> tag, or
// "" when it has none.
func htmlImgAlt(tag string) string {
	m := htmlAltRe.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return resolveEntities([]byte(m[1] + m[2] + m[3]))
}

// imagePlaceholder is the muted text standing in for an image.
func imagePlaceholder(alt string) string {
	if alt == "" {
		return "[image]"
	}
	return "[image: " + alt + "]"
}

// spansText concatenates the text of spans.
func spansText(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// blankLine is the empty line separating blocks.
func blankLine() []Span {
	return []Span{{Text: "", Style: theme.Base}}
}

// trimTrailingSpace removes trailing spaces from line's last spans.
func trimTrailingSpace(line []Span) []Span {
	for len(line) > 0 {
		last := &line[len(line)-1]
		last.Text = strings.TrimRight(last.Text, " ")
		if last.Text != "" {
			break
		}
		line = line[:len(line)-1]
	}
	return line
}
