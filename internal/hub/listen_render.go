package hub

import (
	"fmt"
	"html"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// A passage always quotes the visible source, never a generated explanation.
// Several speech chunks can target one long paragraph without altering its HTML.
type listenPassage struct {
	Target  string `json:"target"`
	Text    string `json:"text"`
	Heading bool   `json:"heading,omitempty"`
}
type listenTarget struct {
	ast.BaseBlock
	id string
}

var kindListenTarget = ast.NewNodeKind("ListenTarget")

func (n *listenTarget) Kind() ast.NodeKind   { return kindListenTarget }
func (n *listenTarget) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

type listenTransformer struct{ passages *[]listenPassage }

func (t *listenTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var nodes []ast.Node
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHeading, ast.KindParagraph, ast.KindTextBlock, ast.KindCodeBlock, ast.KindFencedCodeBlock, east.KindTable, kindMathBlock:
			nodes = append(nodes, n)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, n := range nodes {
		speech := strings.Join(strings.Fields(listenText(n, source)), " ")
		if speech == "" {
			continue
		}
		id := fmt.Sprintf("listen-%d", len(*t.passages))
		for _, chunk := range listenChunks(speech, 1000) {
			*t.passages = append(*t.passages, listenPassage{Target: id, Text: chunk, Heading: n.Kind() == ast.KindHeading})
		}
		wrapper := &listenTarget{id: id}
		parent := n.Parent()
		parent.ReplaceChild(parent, n, wrapper)
		wrapper.AppendChild(wrapper, n)
	}
}
func listenText(n ast.Node, source []byte) string {
	switch v := n.(type) {
	case *ast.Text:
		s := string(v.Segment.Value(source))
		if !v.IsRaw() {
			s = html.UnescapeString(string(util.UnescapePunctuations([]byte(s))))
		}
		if v.SoftLineBreak() || v.HardLineBreak() {
			s += " "
		}
		return s
	case *ast.String:
		return string(v.Value)
	case *wikiLinkNode:
		return v.Label
	case *mathInlineNode:
		return v.tex
	case *mathBlockNode:
		return v.tex
	case *ast.FencedCodeBlock:
		return string(v.Lines().Value(source))
	case *ast.CodeBlock:
		return string(v.Lines().Value(source))
	case *ast.RawHTML, *ast.HTMLBlock:
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		b.WriteString(listenText(c, source))
		if c.Type() == ast.TypeBlock {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Keep UTF-8 intact and prefer sentence/word boundaries. Limit counts runes
// conservatively enough to stay below the speech service's request limit.
func listenChunks(s string, limit int) []string {
	r := []rune(s)
	var out []string
	for len(r) > limit {
		end := limit
		for i := limit; i > limit/2; i-- {
			if unicode.IsSpace(r[i]) {
				end = i
				if strings.ContainsRune(".!?。！？", r[i-1]) {
					break
				}
			}
		}
		// Prefer the last word boundary when no sentence ending was found.
		if end < limit && !strings.ContainsRune(".!?。！？", r[end-1]) {
			for i := limit; i > limit/2; i-- {
				if unicode.IsSpace(r[i]) {
					end = i
					break
				}
			}
		}
		out = append(out, strings.TrimSpace(string(r[:end])))
		r = r[end:]
		for len(r) > 0 && unicode.IsSpace(r[0]) {
			r = r[1:]
		}
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

type listenRenderer struct{}

func (r *listenRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindListenTarget, r.render)
}
func (r *listenRenderer) render(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		fmt.Fprintf(w, `<div class="listen-passage" data-listen-target="%s">`, n.(*listenTarget).id)
	} else {
		w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}
