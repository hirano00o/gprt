package widget

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark/ast"

	"github.com/hirano00o/gprt/internal/kitty"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// ImageStatus is what an ImageSource knows about one image.
type ImageStatus int

// Known image statuses.
const (
	// ImageText: draw the muted text placeholder only. It covers every case
	// where there will be no picture: images disabled, unsupported, or failed.
	ImageText ImageStatus = iota
	// ImageLoading: the picture is on its way; the placeholder says so.
	ImageLoading
	// ImageReady: the picture is available as an ImageResult placement.
	ImageReady
)

// ImageResult is an ImageSource's answer for one image. For ImageReady, the
// image is drawn as Cols x Rows placeholder cells whose foreground colour is
// the terminal image id ID (see package kitty).
type ImageResult struct {
	Status         ImageStatus
	ID, Cols, Rows int
}

// ImageSource supplies the images RenderMarkdown draws. Image is called once
// for each image that stands alone on its line, with src exactly as written
// in the text, the number of columns available, and the width in pixels the
// text asked for (an HTML width attribute; 0 when absent). It is called
// while the UI draws, so it must be quick and must not block.
type ImageSource interface {
	Image(src string, maxCols, widthPx int) ImageResult
}

// imageRef is one image found in the text.
type imageRef struct {
	src, alt string
	widthPx  int
}

// imageLines renders ref: placeholder cells when its picture is ready,
// otherwise the muted text placeholder (with "(loading…)" while it loads).
func (r *mdRenderer) imageLines(ref imageRef, width int) [][]Span {
	text := imagePlaceholder(ref.alt)
	if r.images != nil && ref.src != "" {
		res := r.images.Image(ref.src, width, ref.widthPx)
		switch res.Status {
		case ImageReady:
			style := theme.Base.Foreground(tcell.PaletteColor(res.ID))
			rows := kitty.Rows(min(res.Cols, width), res.Rows)
			lines := make([][]Span, len(rows))
			for i, row := range rows {
				lines[i] = []Span{{Text: row, Style: style}}
			}
			return lines
		case ImageLoading:
			text += " (loading…)"
		}
	}
	return WrapSpans([]Span{{Text: text, Style: theme.Muted}}, width)
}

// splitInlineLines groups n's inline children by source line, a child that
// ends a line (a soft or hard break, or <br>) being the last of its group.
func splitInlineLines(n ast.Node) [][]ast.Node {
	var lines [][]ast.Node
	var cur []ast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		cur = append(cur, c)
		if t, ok := c.(*ast.Text); ok && (t.SoftLineBreak() || t.HardLineBreak()) {
			lines, cur = append(lines, cur), nil
		}
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// standaloneImage reports whether the inline nodes of one line hold exactly
// one image and nothing visible besides it. Whitespace and HTML tags other
// than <img> (<br>, or the <a> wrapping a badge) are invisible, and a link
// around the image still leaves it standing alone.
func (r *mdRenderer) standaloneImage(line []ast.Node) (imageRef, bool) {
	var found []imageRef
	for _, n := range line {
		switch n := n.(type) {
		case *ast.Image:
			found = append(found, r.markdownImage(n))
		case *ast.Link:
			inner, ok := r.standaloneImage(childNodes(n))
			if !ok {
				return imageRef{}, false
			}
			found = append(found, inner)
		case *ast.RawHTML:
			if raw := r.rawHTML(n); htmlImgRe.MatchString(raw) {
				found = append(found, htmlImgRef(raw))
			}
		case *ast.Text:
			if strings.TrimSpace(string(n.Segment.Value(r.src))) != "" {
				return imageRef{}, false
			}
		default:
			return imageRef{}, false
		}
	}
	if len(found) != 1 {
		return imageRef{}, false
	}
	return found[0], true
}

func childNodes(n ast.Node) []ast.Node {
	var out []ast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, c)
	}
	return out
}

func (r *mdRenderer) markdownImage(n *ast.Image) imageRef {
	return imageRef{src: decodeText(n.Destination), alt: r.plain(n)}
}

// htmlImgRef reads the src, alt, and width attributes of an <img> tag. A
// width that is not a plain pixel count (a percentage, say) is ignored.
func htmlImgRef(tag string) imageRef {
	attrs := htmlAttrs(tag)
	ref := imageRef{src: attrs["src"], alt: attrs["alt"]}
	if px, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(attrs["width"]), "px")); err == nil && px > 0 {
		ref.widthPx = px
	}
	return ref
}

// htmlStandaloneImage reports whether an HTML-block line shows one <img> and
// nothing else visible.
func htmlStandaloneImage(line string) (imageRef, bool) {
	matches := htmlImgRe.FindAllStringIndex(line, -1)
	if len(matches) != 1 {
		return imageRef{}, false
	}
	rest := line[:matches[0][0]] + line[matches[0][1]:]
	if strings.TrimSpace(resolveEntities([]byte(htmlTagRe.ReplaceAllString(rest, "")))) != "" {
		return imageRef{}, false
	}
	return htmlImgRef(line[matches[0][0]:matches[0][1]]), true
}
