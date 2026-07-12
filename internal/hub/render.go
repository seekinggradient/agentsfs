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
func renderMarkdown(content string, resolve func(target string) (url string, ok bool), imageResolvers ...func(target string) (url string, ok bool)) (string, error) {
	parserOptions := []parser.Option{parser.WithAutoHeadingID()}
	if len(imageResolvers) > 0 && imageResolvers[0] != nil {
		parserOptions = append(parserOptions, parser.WithASTTransformers(
			util.Prioritized(&imageURLTransformer{resolve: imageResolvers[0]}, 100),
		))
	}
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			// Emit token classes (not inline colors) so our CSS can theme code
			// for both light and dark mode.
			highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
			&wikiLinkExtension{resolve: resolve},
		),
		goldmark.WithParserOptions(parserOptions...),
		goldmark.WithRendererOptions(ghtml.WithHardWraps()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(stripFrontmatter(content)), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// imageURLTransformer rewrites repository-relative Markdown image paths to the
// Hub's raw-file route. The Markdown remains portable on disk (for example,
// ![Damage](media/damage.jpg)), while the browser receives image bytes instead
// of the HTML file-view page for that repository path.
type imageURLTransformer struct {
	resolve func(target string) (url string, ok bool)
}

func (t *imageURLTransformer) Transform(node *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		image, ok := n.(*ast.Image)
		if !ok {
			return ast.WalkContinue, nil
		}
		if resolved, ok := t.resolve(string(image.Destination)); ok {
			image.Destination = []byte(resolved)
		}
		return ast.WalkContinue, nil
	})
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
