package hub

import (
	"strings"
	"testing"
	"time"
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
