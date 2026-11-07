package widget

import (
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"

	"github.com/hirano00o/gprt/internal/ui/theme"
)

// htmlBreakRe matches the inline HTML line break tags <br>, <br/>, <br />.
var htmlBreakRe = regexp.MustCompile(`(?i)^<br\s*/?>$`)

// inline renders n's inline children as Spans in style. A line break (soft,
// hard, or <br>) is a "\n" Span, which WrapSpans turns into a hard break.
func (r *mdRenderer) inline(n ast.Node, style tcell.Style) []Span {
	var out []Span
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, r.inlineNode(c, style)...)
	}
	return out
}

func (r *mdRenderer) inlineNode(n ast.Node, style tcell.Style) []Span {
	switch n := n.(type) {
	case *ast.Text:
		value := n.Segment.Value(r.src)
		s := string(value)
		if !n.IsRaw() {
			s = decodeText(value)
		}
		spans := []Span{{Text: s, Style: style}}
		// Soft breaks are hard breaks too: GitHub renders PR and comment
		// bodies with every newline preserved.
		if n.SoftLineBreak() || n.HardLineBreak() {
			spans = append(spans, Span{Text: "\n", Style: style})
		}
		return spans
	case *ast.String:
		return []Span{{Text: string(n.Value), Style: style}}
	case *ast.CodeSpan:
		var b strings.Builder
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				b.WriteString(strings.ReplaceAll(string(t.Segment.Value(r.src)), "\n", " "))
			}
		}
		return []Span{{Text: b.String(), Style: overlay(style, theme.Code)}}
	case *ast.Emphasis:
		if n.Level >= 2 {
			return r.inline(n, style.Bold(true))
		}
		return r.inline(n, style.Italic(true))
	case *extast.Strikethrough:
		return r.inline(n, style.StrikeThrough(true))
	case *ast.Link:
		spans := r.inline(n, overlay(style, theme.Link))
		dest := decodeText(n.Destination)
		if dest != "" && dest != r.plain(n) {
			spans = append(spans, Span{Text: " (" + dest + ")", Style: theme.Muted})
		}
		return spans
	case *ast.AutoLink:
		return []Span{{Text: string(n.Label(r.src)), Style: overlay(style, theme.Link)}}
	case *ast.Image:
		label := "[image]"
		if alt := r.plain(n); alt != "" {
			label = "[image: " + alt + "]"
		}
		return []Span{{Text: label, Style: theme.Muted}}
	case *ast.RawHTML:
		var b strings.Builder
		for i := range n.Segments.Len() {
			seg := n.Segments.At(i)
			b.Write(seg.Value(r.src))
		}
		if htmlBreakRe.MatchString(b.String()) {
			return []Span{{Text: "\n", Style: style}}
		}
		return nil
	case *extast.TaskCheckBox:
		return nil // shown as the list item's marker instead
	default:
		return r.inline(n, style)
	}
}

// plain returns n's descendant text with no styling, for comparing a link's
// text to its URL and for an image's alt text.
func (r *mdRenderer) plain(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			b.WriteString(decodeText(c.Segment.Value(r.src)))
		case *ast.String:
			b.Write(c.Value)
		default:
			b.WriteString(r.plain(c))
		}
	}
	return b.String()
}

// overlay returns base with over's foreground colour and attributes added:
// the way a link or code span keeps the bold/italic of the emphasis around
// it, and a heading's bold, without taking over's background. The underline
// is carried by its style, not just AttrUnderline: tcell's terminal screen
// draws underlines from the style alone and ignores the deprecated
// attribute bit.
func overlay(base, over tcell.Style) tcell.Style {
	fg, _, overAttr := over.Decompose()
	_, _, baseAttr := base.Decompose()
	out := base.Foreground(fg).Attributes(baseAttr | overAttr)
	if us := over.GetUnderlineStyle(); us != tcell.UnderlineStyleNone {
		out = out.Underline(us)
	}
	return out
}

// decodeText resolves Markdown backslash escapes and HTML character
// references in source text in a single left-to-right pass, as goldmark's
// own HTML writer does: applying the two decoders one after the other would
// decode "\&amp;" (a literal "&amp;") or "&#92;*" (a literal "\*") wrongly.
func decodeText(source []byte) string {
	var b strings.Builder
	start := 0
	for i := 0; i+1 < len(source); i++ {
		if source[i] == '\\' && util.IsPunct(source[i+1]) {
			b.WriteString(resolveEntities(source[start:i]))
			b.WriteByte(source[i+1])
			i++
			start = i + 1
		}
	}
	b.WriteString(resolveEntities(source[start:]))
	return b.String()
}

// resolveEntities decodes HTML numeric and named character references.
func resolveEntities(source []byte) string {
	return string(util.ResolveNumericReferences(util.ResolveEntityNames(source)))
}
