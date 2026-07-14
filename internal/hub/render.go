package hub

import (
	"bytes"
	"html"
	"net/url"
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
	rendererOptions := []renderer.Option{ghtml.WithHardWraps()}
	if len(imageResolvers) > 0 && imageResolvers[0] != nil {
		rendererOptions = append(rendererOptions, renderer.WithNodeRenderers(
			util.Prioritized(&markdownImageRenderer{resolve: imageResolvers[0]}, 100),
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
		goldmark.WithRendererOptions(rendererOptions...),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(stripFrontmatter(content)), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// markdownImageRenderer rewrites repository-relative Markdown image paths to
// the Hub's raw-file route. When a repository image is missing from the current
// version it renders a deliberate inline fallback instead of exposing the
// browser's broken-image icon and an unbounded alt-text line.
type markdownImageRenderer struct {
	resolve func(target string) (url string, ok bool)
}

func (r *markdownImageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, r.renderImage)
}

func (r *markdownImageRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	image := node.(*ast.Image)
	target := strings.TrimSpace(string(image.Destination))
	alt := strings.TrimSpace(string(image.Text(source)))
	if alt == "" {
		alt = "Repository image"
	}
	if resolved, ok := r.resolve(target); ok {
		target = resolved
	} else if isRepositoryImageTarget(target) {
		_, _ = w.WriteString(`<span class="markdown-image-missing" role="img" aria-label="` + html.EscapeString("Image unavailable: "+alt) + `"><span class="markdown-image-missing-mark" aria-hidden="true">×</span><span><strong>Image unavailable</strong><small>` + html.EscapeString(target) + ` is not present in this version</small></span></span>`)
		return ast.WalkSkipChildren, nil
	}

	destination := util.URLEscape([]byte(target), true)
	_, _ = w.WriteString(`<img src="`)
	if !ghtml.IsDangerousURL(destination) {
		_, _ = w.Write(util.EscapeHTML(destination))
	}
	_, _ = w.WriteString(`" alt="` + html.EscapeString(alt) + `" loading="lazy" decoding="async"`)
	if len(image.Title) > 0 {
		_, _ = w.WriteString(` title="` + html.EscapeString(string(image.Title)) + `"`)
	}
	_ = w.WriteByte('>')
	return ast.WalkSkipChildren, nil
}

func isRepositoryImageTarget(target string) bool {
	u, err := url.Parse(strings.TrimSpace(target))
	return err == nil && u.Path != "" && !u.IsAbs() && u.Host == "" && !strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "#")
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
