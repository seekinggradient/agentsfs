package hub

import (
	"encoding/base64"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Bundle presentation assets from the already-authorized repository. No network
// requests, cross-repository reads, or executable resource types are allowed.
// The renderer sanitizes the resulting HTML and applies its own restrictive CSP.
func guidedHTML(bare, sourcePath string) string {
	size, ok := BlobSize("git", bare, defaultRef, sourcePath)
	if !ok || size > maxMdtoBytes {
		return ""
	}
	source, ok := BlobContent("git", bare, defaultRef, sourcePath)
	if !ok || !utf8.ValidString(source) {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	budget := int64(maxMdtoBytes) - int64(len(source))
	readAsset := func(ref string, styles bool) (string, string) {
		u, err := url.Parse(ref)
		if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "\\") {
			return "", ""
		}
		resolved, safe := safeRepoPath(path.Join(path.Dir(sourcePath), u.Path))
		if !safe {
			return "", ""
		}
		mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml"}[strings.ToLower(path.Ext(resolved))]
		if styles {
			if strings.ToLower(path.Ext(resolved)) != ".css" {
				return "", ""
			}
			mime = "text/css"
		}
		if mime == "" {
			return "", ""
		}
		n, exists := BlobSize("git", bare, defaultRef, resolved)
		if !exists || n > budget*3/4 {
			return "", ""
		}
		bytes, exists := BlobContent("git", bare, defaultRef, resolved)
		if !exists {
			return "", ""
		}
		budget -= int64(len(bytes))*4/3 + 64
		return bytes, mime
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "img" {
			for i, attr := range node.Attr {
				if attr.Key != "src" {
					continue
				}
				if bytes, mime := readAsset(attr.Val, false); mime != "" {
					node.Attr[i].Val = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(bytes))
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "link" {
			var rel, href string
			for _, attr := range node.Attr {
				if attr.Key == "rel" {
					rel = attr.Val
				}
				if attr.Key == "href" {
					href = attr.Val
				}
			}
			if rel == "stylesheet" {
				if css, mime := readAsset(href, true); mime != "" {
					node.Data = "style"
					node.Attr = nil
					node.AppendChild(&html.Node{Type: html.TextNode, Data: css})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	var out strings.Builder
	if html.Render(&out, doc) != nil {
		return ""
	}
	if out.Len() > maxMdtoBytes {
		return ""
	}
	return out.String()
}
