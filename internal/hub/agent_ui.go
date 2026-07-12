package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"agentsfs.ai/afs/internal/hub/agentbundle"
)

type agentRouteKind uint8

const (
	agentRouteUI agentRouteKind = iota + 1
	agentRouteAPI
	agentRoutePreview
)

type agentRoute struct {
	kind        agentRouteKind
	allow       string
	contentType string
}

var (
	agentConversationID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
	agentInlineScript   = regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	agentUIBundleFiles  = map[string]string{
		"/":           "src/web/index.html",
		"/index.html": "src/web/index.html",
		"/app.js":     "src/web/app.js",
		"/lib.js":     "src/web/lib.js",
		"/styles.css": "src/web/styles.css",
	}
	agentUIAssets = mustLoadAgentUI(agentBundle)
)

type agentUIAsset struct {
	body        []byte
	contentType string
	etag        string
}

func classifyAgentPath(p string) (agentRoute, bool) {
	if _, ok := agentUIBundleFiles[p]; ok {
		return agentRoute{kind: agentRouteUI, allow: "GET, HEAD"}, true
	}
	// Hub previews are self-contained by contract. Only the index document is
	// reachable: allowing Sprite-controlled .js/.css subpaths on the Hub origin
	// would let any future DOM injection bypass the immutable UI boundary.
	if p == "/preview" || p == "/preview/" {
		return agentRoute{kind: agentRoutePreview, allow: "GET, HEAD"}, true
	}
	switch p {
	case "/api/health", "/api/config", "/api/instances":
		return agentRoute{kind: agentRouteAPI, allow: "GET", contentType: "application/json; charset=utf-8"}, true
	case "/api/instance", "/api/focus", "/api/tools/call", "/api/realtime/token",
		"/api/review/commit", "/api/review/discard":
		return agentRoute{kind: agentRouteAPI, allow: "POST", contentType: "application/json; charset=utf-8"}, true
	case "/api/chat":
		return agentRoute{kind: agentRouteAPI, allow: "POST", contentType: "text/event-stream; charset=utf-8"}, true
	case "/api/conversations":
		return agentRoute{kind: agentRouteAPI, allow: "GET, POST", contentType: "application/json; charset=utf-8"}, true
	}
	const conversations = "/api/conversations/"
	if strings.HasPrefix(p, conversations) && agentConversationID.MatchString(strings.TrimPrefix(p, conversations)) {
		return agentRoute{kind: agentRouteAPI, allow: "GET, DELETE", contentType: "application/json; charset=utf-8"}, true
	}
	return agentRoute{}, false
}

func (route agentRoute) allows(method string) bool {
	for _, allowed := range strings.Split(route.allow, ", ") {
		if method == allowed {
			return true
		}
	}
	return false
}

func relativeAgentPath(fullPath, prefix string) (string, bool) {
	if fullPath == prefix {
		return "/", true
	}
	if !strings.HasPrefix(fullPath, prefix+"/") {
		return "", false
	}
	return strings.TrimPrefix(fullPath, prefix), true
}

func enforceAgentRoute(w http.ResponseWriter, method, p string, proxyOnly bool) (agentRoute, bool) {
	route, ok := classifyAgentPath(p)
	if !ok || (proxyOnly && route.kind == agentRouteUI) {
		http.Error(w, "not found", http.StatusNotFound)
		return agentRoute{}, false
	}
	if !route.allows(method) {
		w.Header().Set("Allow", route.allow)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return agentRoute{}, false
	}
	return route, true
}

func mustLoadAgentUI(bundle []byte) map[string]agentUIAsset {
	assets, err := loadAgentUI(bundle)
	if err != nil {
		panic("load embedded agent UI: " + err.Error())
	}
	return assets
}

func loadAgentUI(bundle []byte) (map[string]agentUIAsset, error) {
	if err := agentbundle.Validate(bytes.NewReader(bundle)); err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	wanted := make(map[string]bool)
	for _, name := range agentUIBundleFiles {
		wanted[name] = true
	}
	files := make(map[string][]byte)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !wanted[hdr.Name] {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[hdr.Name] = data
	}

	assets := make(map[string]agentUIAsset, len(agentUIBundleFiles))
	for routePath, bundlePath := range agentUIBundleFiles {
		data, ok := files[bundlePath]
		if !ok {
			return nil, fmt.Errorf("bundle is missing agent UI asset %q", bundlePath)
		}
		sum := sha256.Sum256(data)
		asset := agentUIAsset{
			body:        append([]byte(nil), data...),
			contentType: agentUIContentType(routePath),
			etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
		}
		assets[routePath] = asset
	}
	return assets, nil
}

func agentUIContentType(p string) string {
	switch path.Ext(p) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	default:
		return "text/html; charset=utf-8"
	}
}

func agentUICSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "' 'strict-dynamic'" +
		"; style-src 'self' 'unsafe-inline'" +
		"; img-src 'self' data: blob:" +
		"; font-src 'self' data:" +
		"; media-src 'self' blob:" +
		"; connect-src 'self' https://api.openai.com" +
		"; frame-src 'none'; worker-src 'none'; object-src 'none'" +
		"; base-uri 'none'; frame-ancestors 'self'; form-action 'none'"
}

func nonceAgentUIScripts(index []byte) ([]byte, string, error) {
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, "", err
	}
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	starts := bytes.Count(index, []byte("<script"))
	if starts == 0 || starts != len(agentInlineScript.FindAll(index, -1)) {
		return nil, "", fmt.Errorf("embedded agent UI has unsupported script markup")
	}
	replacement := []byte(`<script nonce="` + nonce + `"`)
	return bytes.ReplaceAll(index, []byte("<script"), replacement), nonce, nil
}

func serveAgentUI(w http.ResponseWriter, r *http.Request, p string) {
	asset, ok := agentUIAssets[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body := asset.body
	isDocument := p == "/" || p == "/index.html"
	if isDocument {
		var nonce string
		var err error
		body, nonce, err = nonceAgentUIScripts(asset.body)
		if err != nil {
			http.Error(w, "agent UI unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Security-Policy", agentUICSP(nonce))
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("ETag", asset.etag)
		if r.Header.Get("If-None-Match") == asset.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func previewContentType(p string) string {
	if p == "/preview" || p == "/preview/" {
		return "text/html; charset=utf-8"
	}
	return "application/octet-stream"
}
