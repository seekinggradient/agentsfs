package hub

import (
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdownLetsSoftLineBreaksReflow(t *testing.T) {
	html, err := renderMarkdown(
		"Editorial prose is commonly wrapped in the source\nbut should flow continuously in the reading column.\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<br") {
		t.Fatalf("soft source newline rendered as a forced line break:\n%s", html)
	}
	if !strings.Contains(html, "source\nbut should flow") {
		t.Fatalf("renderer unexpectedly changed the paragraph text:\n%s", html)
	}
}

func TestRenderMarkdownPreservesExplicitHardBreaks(t *testing.T) {
	for _, tc := range []struct {
		name, markdown string
	}{
		{name: "trailing spaces", markdown: "First line.  \nSecond line.\n"},
		{name: "backslash", markdown: "First line.\\\nSecond line.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := renderMarkdown(tc.markdown, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html, "<br") {
				t.Fatalf("explicit Markdown hard break was not preserved:\n%s", html)
			}
		})
	}
}

func TestRenderMarkdownRewritesRepositoryImage(t *testing.T) {
	html, err := renderMarkdown(
		"# Inspection\n\n![Damaged cabinet](media/kitchen-sink.jpg)\n\n![External](https://example.com/photo.jpg)\n",
		func(string) (string, bool) { return "", false },
		func(target string) (string, bool) {
			if target == "media/kitchen-sink.jpg" {
				return "/alice/claim/raw/projects/media/kitchen-sink.jpg", true
			}
			return "", false
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`src="/alice/claim/raw/projects/media/kitchen-sink.jpg"`,
		`alt="Damaged cabinet"`,
		`src="https://example.com/photo.jpg"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Markdown missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownShowsRepositoryImageFallback(t *testing.T) {
	html, err := renderMarkdown(
		"Before\n\n![Inspection photo](media/missing.png)\n\nAfter\n",
		func(string) (string, bool) { return "", false },
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="markdown-image-missing"`,
		`Image unavailable`,
		`media/missing.png is not present in this version`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing-image fallback missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `<img src="media/missing.png"`) {
		t.Fatalf("missing repository image rendered as a broken img element:\n%s", html)
	}
}

func TestRenderMarkdownContainsWideTablesInScrollableRegion(t *testing.T) {
	html, err := renderMarkdown(
		"| Category | Item | Qty | Source | Status | Notes |\n|---|---|---|---|---|---|\n| Swim | Rash guards | 3 | Local | Needed | Try on |\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<div class="prose-table-scroll" role="region" aria-label="Scrollable table" tabindex="0"><table>`,
		`</table></div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Markdown table missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownRendersInlineAndDisplayMath(t *testing.T) {
	html, err := renderMarkdown(
		"Linear attention computes $Q(K^\\top V)$ instead.\n\n$$ Q(K^\\top V) $$\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="math-textstyle"`,
		`display="inline"`,
		`<span class="math-display">`,
		`class="math-displaystyle"`,
		`display="block"`,
		`displaystyle="true"`,
		`<msup>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered math missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "$Q(") || strings.Contains(html, "$$") {
		t.Fatalf("rendered math leaked TeX delimiters:\n%s", html)
	}
}

func TestRenderMarkdownRendersLongMultilineMathAsOneBlock(t *testing.T) {
	html, err := renderMarkdown(
		"Before.\n\n$$\n\\alpha_{i\\rightarrow l} =\n\\frac{\n\\exp\\left(w_l^\\top k_i\\right)\n}{\n\\sum_{j<l}\\exp\\left(w_l^\\top k_j\\right)\n}\n$$\n\nAfter.\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<h1") || strings.Contains(html, "$$") {
		t.Fatalf("multiline math was parsed as ordinary Markdown:\n%s", html)
	}
	if strings.Contains(html, "<merror") || strings.Contains(html, "math-error") {
		t.Fatalf("multiline math produced a rendering error:\n%s", html)
	}
	for _, want := range []string{`class="math-display"`, `<mfrac>`, `<munder>`} {
		if !strings.Contains(html, want) {
			t.Errorf("multiline math missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownSupportsKimiNoteMathVocabulary(t *testing.T) {
	formulas := []string{
		`X \in \mathbb{R}^{N \times d}`,
		`\operatorname{Attention}(Q,K,V) = \operatorname{softmax}\left(\frac{QK^\top}{\sqrt{d_k}} + M\right)V`,
		`\operatorname{LinearAttention}(Q,K,V) = \frac{\phi(Q)\left(\phi(K)^\top V\right)}{\phi(Q)\left(\phi(K)^\top \mathbf{1}\right)}`,
		`S_1 = k_1v_1^\top = \begin{bmatrix}1 & 0\\0 & 0\end{bmatrix}`,
		`\mathcal{L}_t(S)=\frac{1}{2}\left|S^\top k_t-v_t\right|^2`,
		`\hat v_t \mapsto v_t-\beta_t\hat v_t,\qquad \text{durable features}`,
		`S_1 \rightarrow S_2 \rightarrow S_3 \rightarrow \cdots \rightarrow S_N`,
		`y_t = W_O\left[\operatorname{sigmoid}(\cdot)\odot\operatorname{RMSNorm}(\tilde o_t)\right]`,
		`\alpha_t=\exp(g_t),\qquad g_{\min}=-5`,
	}
	var markdown strings.Builder
	for _, formula := range formulas {
		markdown.WriteString("$$\n")
		markdown.WriteString(formula)
		markdown.WriteString("\n$$\n\n")
	}
	html, err := renderMarkdown(markdown.String(), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<merror") || strings.Contains(html, "math-error") {
		t.Fatalf("Kimi note math vocabulary produced a rendering error:\n%s", html)
	}
	if got := strings.Count(html, `<math `); got != len(formulas) {
		t.Fatalf("rendered %d math expressions, want %d:\n%s", got, len(formulas), html)
	}
	if strings.Contains(html, `>mathrm{`) {
		t.Fatalf(`\operatorname emitted a literal nested \mathrm command:
%s`, html)
	}
	for _, want := range []string{`>Attention</mo>`, `>softmax</mo>`, `>RMSNorm</mo>`} {
		if !strings.Contains(html, want) {
			t.Errorf(`\operatorname output missing %q:
%s`, want, html)
		}
	}
}

func TestRenderMarkdownDoesNotTreatCurrencyOrCodeAsMath(t *testing.T) {
	html, err := renderMarkdown(
		"Tickets cost $5 and lunch costs $10. Keep `$Q(K^\\top V)$` as code.\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<math") {
		t.Fatalf("currency or code was treated as math:\n%s", html)
	}
	for _, want := range []string{`$5 and lunch costs $10`, `$Q(K^\top V)$`} {
		if !strings.Contains(html, want) {
			t.Errorf("ordinary dollar text missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownMathCannotInjectHTML(t *testing.T) {
	html, err := renderMarkdown(
		"$\\text{<img src=x onerror=alert(1)>}$\n",
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<img") {
		t.Fatalf("math rendered unsafe HTML:\n%s", html)
	}
	if !strings.Contains(html, `class="math-error"`) || !strings.Contains(html, "&lt;img") {
		t.Fatalf("unsafe math did not fall back to escaped source:\n%s", html)
	}
}

func TestAgeStringUsesMinutesForRecentChanges(t *testing.T) {
	got := ageString(time.Now().Add(-27 * time.Minute).Unix())
	if got != "27m ago" {
		t.Fatalf("ageString() = %q, want 27m ago", got)
	}
}

func TestParseDiffLinesClassifiesPatchRows(t *testing.T) {
	lines := parseDiffLines("diff --git a/NOTE.md b/NOTE.md\n@@ -1 +1 @@\n-old\n+new\n")
	if len(lines) != 4 {
		t.Fatalf("got %d diff lines, want 4", len(lines))
	}
	want := []struct{ kind, mark string }{{"meta", "·"}, {"hunk", "·"}, {"remove", "−"}, {"add", "+"}}
	for i, w := range want {
		if lines[i].Kind != w.kind || lines[i].Mark != w.mark {
			t.Errorf("line %d = %+v, want kind=%q mark=%q", i, lines[i], w.kind, w.mark)
		}
	}
}
