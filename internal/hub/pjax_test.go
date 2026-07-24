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
