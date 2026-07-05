package hub

import (
	"bytes"
	"html"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// renderMarkdown turns a note's markdown into safe HTML: GFM + syntax
// highlighting + agentsfs [[wikilinks]] resolved to hub URLs. Raw HTML in the
// source is escaped (goldmark is not put in unsafe mode), so a note can never
// inject markup — the content is user data, not trusted.
//
// resolve maps a wikilink target to a hub URL, reporting whether it resolved.
func renderMarkdown(content string, resolve func(target string) (url string, ok bool)) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			// Emit token classes (not inline colors) so our CSS can theme code
			// for both light and dark mode.
			highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
			&wikiLinkExtension{resolve: resolve},
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(ghtml.WithHardWraps()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(stripFrontmatter(content)), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// stripFrontmatter removes a leading YAML frontmatter block so it isn't shown
// as body text; the description is surfaced separately in the UI.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return content
}

// ---- [[wikilink]] goldmark extension ----

var kindWikiLink = ast.NewNodeKind("WikiLink")

type wikiLinkNode struct {
	ast.BaseInline
	Label    string
	URL      string
	Resolved bool
}

func (n *wikiLinkNode) Kind() ast.NodeKind         { return kindWikiLink }
func (n *wikiLinkNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type wikiLinkExtension struct {
	resolve func(target string) (string, bool)
}

func (e *wikiLinkExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(&wikiLinkParser{resolve: e.resolve}, 100),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&wikiLinkRenderer{}, 100),
	))
}

type wikiLinkParser struct {
	resolve func(target string) (string, bool)
}

func (p *wikiLinkParser) Trigger() []byte { return []byte{'['} }

func (p *wikiLinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 4 || line[0] != '[' || line[1] != '[' {
		return nil
	}
	end := bytes.Index(line, []byte("]]"))
	if end < 2 {
		return nil
	}
	inner := string(line[2:end])
	target, label := inner, inner
	if i := strings.Index(inner, "|"); i >= 0 {
		target, label = inner[:i], inner[i+1:]
	}
	url, ok := p.resolve(strings.TrimSpace(target))
	block.Advance(end + 2)
	return &wikiLinkNode{Label: strings.TrimSpace(label), URL: url, Resolved: ok}
}

type wikiLinkRenderer struct{}

func (r *wikiLinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindWikiLink, r.render)
}

func (r *wikiLinkRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*wikiLinkNode)
	label := html.EscapeString(n.Label)
	if n.Resolved {
		w.WriteString(`<a class="wl" href="` + html.EscapeString(n.URL) + `">` + label + `</a>`)
	} else {
		w.WriteString(`<span class="wl wl-missing" title="no matching file">` + label + `</span>`)
	}
	return ast.WalkSkipChildren, nil
}
