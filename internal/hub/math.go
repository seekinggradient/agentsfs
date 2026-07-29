package hub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wyatt915/treeblood"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mathExtension renders TeX-style $...$ and $$...$$ regions as native
// MathML. Rendering happens on the server, so notes do not depend on a
// third-party script or a client-side typesetting pass.
type mathExtension struct {
	document *treeblood.Pitziil
}

func newMathExtension() goldmark.Extender {
	// Treeblood intentionally implements a compact TeX vocabulary.
	// \operatorname is common in technical Markdown, including the Kimi note,
	// and is equivalent here to a roman-text math operator.
	macros := map[string]string{
		"operatorname": `\mathop{\mathrm{#1}}`,
	}
	return &mathExtension{document: treeblood.NewDocument(macros, false)}
}

func (e *mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(
			util.Prioritized(&mathInlineParser{}, 50),
		),
		parser.WithBlockParsers(
			util.Prioritized(&mathBlockParser{}, 90),
		),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&mathRenderer{document: e.document}, 100),
	))
}

type mathInlineNode struct {
	ast.BaseInline
	tex     string
	display bool
	open    string
	close   string
}

func (n *mathInlineNode) Kind() ast.NodeKind { return kindMathInline }
func (n *mathInlineNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mathBlockNode struct {
	ast.BaseBlock
	tex   string
	open  string
	close string
}

func (n *mathBlockNode) Kind() ast.NodeKind { return kindMathBlock }
func (n *mathBlockNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

var (
	kindMathInline = ast.NewNodeKind("MathInline")
	kindMathBlock  = ast.NewNodeKind("MathBlock")
)

type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'$', '\\'} }

func (p *mathInlineParser) Parse(_ ast.Node, reader text.Reader, _ parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	if len(line) == 0 {
		return nil
	}

	open, close, display := mathDelimiters(line)
	if open == "" {
		return nil
	}

	// Multiline display math is owned by mathBlockParser. Inline math must
	// close on the current source line.
	search := line[len(open):]
	end := mathClosingDelimiter(search, close, display)
	if end < 0 {
		return nil
	}
	if !display && !validInlineMathBoundary(search, end) {
		return nil
	}

	start := segment.Start + len(open)
	stop := start + end
	equation := string(reader.Value(text.NewSegment(start, stop)))
	reader.Advance(len(open) + end + len(close))
	return &mathInlineNode{
		tex:     equation,
		display: display,
		open:    open,
		close:   close,
	}
}

func mathDelimiters(line []byte) (open, close string, display bool) {
	switch {
	case bytes.HasPrefix(line, []byte("$$")):
		return "$$", "$$", true
	case bytes.HasPrefix(line, []byte("$")):
		return "$", "$", false
	case bytes.HasPrefix(line, []byte(`\[`)):
		return `\[`, `\]`, true
	case bytes.HasPrefix(line, []byte(`\(`)):
		return `\(`, `\)`, false
	default:
		return "", "", false
	}
}

func mathClosingDelimiter(source []byte, close string, display bool) int {
	closing := []byte(close)
	for offset := 0; offset <= len(source)-len(closing); {
		next := bytes.Index(source[offset:], closing)
		if next < 0 {
			return -1
		}
		next += offset
		if next == 0 || source[next-1] != '\\' || close != "$" {
			if !display && bytes.IndexByte(source[:next], '`') >= 0 {
				return -1
			}
			if display || validMathCloseBoundary(source, next, len(closing)) {
				return next
			}
		}
		offset = next + len(closing)
	}
	return -1
}

func validMathCloseBoundary(source []byte, start, length int) bool {
	if start > 0 && unicode.IsSpace(lastRune(source[:start])) {
		return false
	}
	after := start + length
	if after >= len(source) {
		return true
	}
	next, _ := utf8.DecodeRune(source[after:])
	return !unicode.IsLetter(next) && !unicode.IsDigit(next)
}

func validInlineMathBoundary(source []byte, end int) bool {
	if end == 0 || len(source) == 0 {
		return false
	}
	first, _ := utf8.DecodeRune(source)
	return !unicode.IsSpace(first)
}

func lastRune(source []byte) rune {
	r, _ := utf8.DecodeLastRune(source)
	return r
}

var mathBlockContextKey = parser.NewContextKey()

type mathBlockState struct {
	close string
}

type mathBlockParser struct{}

func (p *mathBlockParser) Trigger() []byte { return []byte{'$', '\\'} }

func (p *mathBlockParser) Open(_ ast.Node, reader text.Reader, context parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	open, close, display := mathDelimiters(line)
	if !display {
		return nil, parser.NoChildren
	}

	// A display expression that opens and closes on one line is handled by
	// the inline parser. Here we claim only genuinely multiline regions.
	if mathClosingDelimiter(line[len(open):], close, true) >= 0 {
		return nil, parser.NoChildren
	}
	if !hasMathBlockClose(reader, close) {
		return nil, parser.NoChildren
	}

	reader.Advance(len(open))
	node := &mathBlockNode{open: open, close: close}
	_, segment := reader.PeekLine()
	node.Lines().Append(segment)
	context.Set(mathBlockContextKey, mathBlockState{close: close})
	return node, parser.NoChildren
}

func hasMathBlockClose(reader text.Reader, close string) bool {
	line, segment := reader.Position()
	defer reader.SetPosition(line, segment)

	reader.AdvanceLine()
	for {
		next, _ := reader.PeekLine()
		if next == nil {
			return false
		}
		if bytes.Contains(next, []byte(close)) {
			return true
		}
		reader.AdvanceLine()
	}
}

func (p *mathBlockParser) Continue(node ast.Node, reader text.Reader, context parser.Context) parser.State {
	state, ok := context.Get(mathBlockContextKey).(mathBlockState)
	if !ok {
		return parser.None
	}

	line, segment := reader.PeekLine()
	if stop := bytes.Index(line, []byte(state.close)); stop >= 0 {
		node.Lines().Append(text.NewSegment(segment.Start, segment.Start+stop))
		reader.Advance(stop + len(state.close))
		return parser.Close | parser.NoChildren
	}
	node.Lines().Append(segment)
	return parser.Continue | parser.NoChildren
}

func (p *mathBlockParser) Close(node ast.Node, reader text.Reader, context parser.Context) {
	if math, ok := node.(*mathBlockNode); ok {
		var equation strings.Builder
		for i := 0; i < math.Lines().Len(); i++ {
			equation.Write(reader.Value(math.Lines().At(i)))
		}
		math.tex = strings.TrimSpace(equation.String())
	}
	context.Set(mathBlockContextKey, nil)
}

func (p *mathBlockParser) CanInterruptParagraph() bool { return true }
func (p *mathBlockParser) CanAcceptIndentedLine() bool { return false }

type mathRenderer struct {
	document *treeblood.Pitziil
}

func (r *mathRenderer) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(kindMathInline, r.render)
	register.Register(kindMathBlock, r.render)
}

func (r *mathRenderer) render(writer util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}

	var equation, open, close string
	var display, block bool
	switch math := node.(type) {
	case *mathInlineNode:
		equation, open, close, display = math.tex, math.open, math.close, math.display
	case *mathBlockNode:
		equation, open, close, display = math.tex, math.open, math.close, true
		block = true
	default:
		return ast.WalkContinue, nil
	}

	var rendered string
	var err error
	if display {
		rendered, err = r.document.DisplayStyle(equation)
	} else {
		rendered, err = r.document.TextStyle(equation)
	}
	if err == nil && strings.Contains(rendered, "<merror") {
		err = fmt.Errorf("unsupported TeX expression")
	}
	if err == nil {
		rendered, err = sanitizeMathML(rendered)
	}
	if err != nil {
		class := "math-error"
		if display {
			class += " math-error-display"
		}
		_, _ = writer.WriteString(`<code class="` + class + `" title="Math could not be rendered">` +
			html.EscapeString(open+equation+close) + `</code>`)
		return ast.WalkSkipChildren, nil
	}

	tag := ""
	if display {
		tag = "span"
		if block {
			tag = "div"
		}
		_, _ = writer.WriteString(`<` + tag + ` class="math-display">`)
	}
	_, _ = writer.WriteString(rendered)
	if tag != "" {
		_, _ = writer.WriteString(`</` + tag + `>`)
	}
	return ast.WalkSkipChildren, nil
}

var allowedMathElements = map[string]bool{
	"annotation": true, "math": true, "menclose": true, "merror": true,
	"mfrac": true, "mi": true, "mlabeledtr": true, "mmultiscripts": true,
	"mn": true, "mo": true, "mover": true, "mpadded": true,
	"mprescripts": true, "mroot": true, "mrow": true, "mspace": true,
	"msqrt": true, "mstyle": true, "msub": true, "msubsup": true,
	"msup": true, "mtable": true, "mtd": true, "mtext": true,
	"mtr": true, "munder": true, "munderover": true, "none": true,
	"semantics": true,
}

var allowedMathAttributes = map[string]bool{
	"accent": true, "class": true, "columnalign": true, "columnlines": true,
	"columnspan": true, "dir": true, "display": true, "displaystyle": true,
	"encoding": true, "fence": true, "form": true, "largeop": true,
	"linebreak": true, "linethickness": true, "lspace": true,
	"mathcolor": true, "mathsize": true, "mathvariant": true, "minsize": true,
	"movablelimits": true, "notation": true, "rowalign": true,
	"rowspacing": true, "rowspan": true, "rspace": true, "scriptlevel": true,
	"stretchy": true, "symmetric": true, "title": true, "voffset": true,
	"width": true,
}

// sanitizeMathML preserves renderMarkdown's core guarantee: repository
// content cannot inject arbitrary HTML. Treeblood emits a small, known MathML
// vocabulary; we parse that output, allowlist its elements and attributes, and
// re-encode every text and attribute value before returning it to the page.
func sanitizeMathML(source string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(source))
	decoder.Strict = false

	var output strings.Builder
	encoder := xml.NewEncoder(&output)
	depth := 0
	seenRoot := false

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch value := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if !allowedMathElements[name] {
				return "", fmt.Errorf("unsupported MathML element %q", name)
			}
			if depth == 0 {
				if seenRoot || name != "math" {
					return "", fmt.Errorf("MathML root must be math")
				}
				seenRoot = true
			}
			depth++
			value.Name = xml.Name{Local: name}
			attributes := value.Attr[:0]
			for _, attribute := range value.Attr {
				attributeName := strings.ToLower(attribute.Name.Local)
				if !allowedMathAttributes[attributeName] {
					continue
				}
				attribute.Name = xml.Name{Local: attributeName}
				attribute.Value = html.UnescapeString(attribute.Value)
				attributes = append(attributes, attribute)
			}
			value.Attr = attributes
			if err := encoder.EncodeToken(value); err != nil {
				return "", err
			}
		case xml.EndElement:
			name := strings.ToLower(value.Name.Local)
			if !allowedMathElements[name] || depth == 0 {
				return "", fmt.Errorf("invalid MathML closing element %q", name)
			}
			depth--
			value.Name = xml.Name{Local: name}
			if err := encoder.EncodeToken(value); err != nil {
				return "", err
			}
		case xml.CharData:
			// Strict=false preserves HTML named entities that XML does not
			// know (for example &imath;). Resolve them to Unicode, then let
			// the XML encoder escape the resulting text safely.
			text := xml.CharData(html.UnescapeString(string(value)))
			if err := encoder.EncodeToken(text); err != nil {
				return "", err
			}
		case xml.Comment:
			// Treeblood does not need comments in its output.
		case xml.Directive, xml.ProcInst:
			return "", fmt.Errorf("unsupported declaration in MathML")
		}
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	if !seenRoot || depth != 0 {
		return "", fmt.Errorf("incomplete MathML document")
	}
	return strings.TrimSpace(output.String()), nil
}
