package hub

import (
	"strings"
	"testing"
)

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
