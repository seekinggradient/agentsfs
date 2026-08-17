package hub

import (
	"strings"
	"testing"
)

// TestPJAXSynchronizesPageShell guards the repo-to-file transition: file
// workspace CSS variables live on body.file-shell, so the fetched body class
// must be applied before the new #page markup is inserted.
func TestPJAXSynchronizesPageShell(t *testing.T) {
	asset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	script := string(asset)
	classSync := strings.Index(script, `document.body.className = doc.body ? doc.body.className : "";`)
	pageSwap := strings.Index(script, `page.innerHTML = newPage.innerHTML;`)
	if classSync < 0 {
		t.Fatal("PJAX navigation does not synchronize the fetched body class")
	}
	if pageSwap < 0 {
		t.Fatal("PJAX page swap not found")
	}
	if classSync > pageSwap {
		t.Fatal("PJAX body class must be synchronized before inserting page markup")
	}
}

// TestPJAXDoesNotAnimateTheWholeWorkspace prevents a regression to the
// full-page dim + entrance animation that made every file click visibly flash.
func TestPJAXDoesNotAnimateTheWholeWorkspace(t *testing.T) {
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	script, style := string(scriptAsset), string(styleAsset)
	if strings.Contains(script, `classList.add("pjax-loading")`) {
		t.Fatal("PJAX navigation must not dim the entire page while fetching")
	}
	if !strings.Contains(script, `page.setAttribute("aria-busy", "true")`) {
		t.Fatal("PJAX navigation should expose a nonvisual loading state")
	}
	if strings.Contains(style, "#page.pjax-loading") || strings.Contains(style, "animation: file-workspace-in") {
		t.Fatal("file navigation must not animate the whole workspace")
	}
}

// TestAgentDockUsesFullPageOnTouchTablets guards the iPad layout boundary.
// Native iPadOS input accessories are positioned outside focused iframes, so a
// landscape iPad must use the same full-page agent route as a narrow screen
// instead of letting the accessory float across the repository pane.
func TestAgentDockUsesFullPageOnTouchTablets(t *testing.T) {
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	script, style := string(scriptAsset), string(styleAsset)
	compactQuery := `(max-width: 1120px), (hover: none) and (pointer: coarse)`
	if !strings.Contains(script, `window.matchMedia("`+compactQuery+`").matches`) {
		t.Fatal("agent navigation does not treat coarse-pointer tablets as compact workspaces")
	}
	if strings.Contains(script, "isPhone") {
		t.Fatal("agent layout still uses the width-only phone boundary")
	}
	if strings.Count(style, "@media "+compactQuery) < 2 {
		t.Fatal("agent dock and review UI do not share the touch-tablet compact boundary")
	}
}

// TestCompactViewsKeepIndependentPreferences prevents a dense desktop table
// preference from stranding a phone on a horizontally clipped first view.
// Phones may still choose Table explicitly; that choice is remembered in the
// compact key instead of overwriting the desktop preference.
func TestCompactViewsKeepIndependentPreferences(t *testing.T) {
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	script := string(scriptAsset)
	for _, want := range []string{
		`function isCompactContentView()`,
		`"afs-dashboard-view" + (isCompactContentView() ? ":compact" : "")`,
		`"afs-repo-view:" + repoScope(location.pathname) + (isCompactContentView() ? ":compact" : "")`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("compact view preference logic missing %q", want)
		}
	}
}

// TestMobileOverflowCuesRemainWired guards the two subtle affordances that are
// easy to lose in a visual refactor: fading clipped note actions and revealing
// the graph-folder hint only when its legend genuinely overflows.
func TestMobileOverflowCuesRemainWired(t *testing.T) {
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	script, style := string(scriptAsset), string(styleAsset)
	for _, want := range []string{`function reflectHorizontalOverflow(element)`, `legendHint.hidden = !(`, `initHorizontalOverflowCues();`} {
		if !strings.Contains(script, want) {
			t.Errorf("overflow cue script missing %q", want)
		}
	}
	resizeRefresh := `requestAnimationFrame(function () {
      initWorkspaceResizers();
      initHorizontalOverflowCues();
    });`
	if !strings.Contains(script, resizeRefresh) {
		t.Error("horizontal overflow cues are not refreshed after viewport changes")
	}
	for _, want := range []string{`.note-actions.has-overflow-right`, `.graph-legend-hint`, `.horizontal-scroll-hint`} {
		if !strings.Contains(style, want) {
			t.Errorf("overflow cue style missing %q", want)
		}
	}
}
