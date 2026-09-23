package hub

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	maxHTMLImageDocument = 2 << 20
	maxHTMLImageBytes    = 2 << 20
	maxHTMLImageOutput   = 16 << 20
	maxHTMLImageLookups  = 128
)

// serveHTMLWithImages is only used after repository-wide read authorization.
// Share links deliberately keep their existing file-scoped serving path.
// Embedding image bytes avoids both /render's HTML-only routing and private
// subresource authentication from the opaque-origin document. SVG stays an
// image, never active markup in the Hub origin; the document CSP is unchanged.
func (s *Server) serveHTMLWithImages(w http.ResponseWriter, user, repo, filePath string) bool {
	bare := s.Storage.RepoDir(user, repo)
	ref := mustGitHead(bare)
	if ref == "" {
		return false
	}
	body, ok := s.htmlImageBlob(user, repo, ref, filePath, maxHTMLImageDocument)
	if !ok {
		// Preserve streaming for large documents and existing missing-LFS errors.
		return s.serveRepoBlob(w, user, repo, filePath, setHTMLRenderHeaders)
	}
	body = inlineHTMLImages(body, filePath, func(rel string) (string, bool) {
		return s.htmlImageBlob(user, repo, ref, rel, maxHTMLImageBytes)
	})
	setHTMLRenderHeaders(w)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = io.WriteString(w, body)
	return true
}

// All reads use the same commit and are size-checked before allocation,
// including objects stored through LFS.
func (s *Server) htmlImageBlob(user, repo, ref, filePath string, limit int64) (string, bool) {
	bare := s.Storage.RepoDir(user, repo)
	size, ok := BlobSize("git", bare, ref, filePath)
	if !ok || size > limit {
		return "", false
	}
	body, ok := BlobContent("git", bare, ref, filePath)
	if !ok {
		return "", false
	}
	if ptr, isPtr := ParseLFSPointer(body); isPtr {
		if s.LFS == nil || ptr.Size > limit {
			return "", false
		}
		r, size, err := s.LFS.Open(user, repo, ptr.OID, ptr.Size)
		if err != nil {
			return "", false
		}
		defer r.Close()
		if size > limit {
			return "", false
		}
		data, err := io.ReadAll(io.LimitReader(r, limit+1))
		return string(data), err == nil && int64(len(data)) <= limit
	}
	return body, true
}

func htmlImagePath(document, src string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" ||
		strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "\\") {
		return "", "", false
	}
	rel := path.Join(path.Dir(document), u.Path)
	if !validRepoPath(rel) {
		return "", "", false
	}
	return rel, u.EscapedFragment(), true
}

func htmlImageType(rel string) string {
	switch strings.ToLower(path.Ext(rel)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	}
	return ""
}

func inlineHTMLImages(body, document string, read func(string) (string, bool)) string {
	z := html.NewTokenizer(strings.NewReader(body))
	var out strings.Builder
	cache := make(map[string]string)
	lookups, added, cached := 0, 0, 0
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			out.Write(z.Raw())
			return out.String()
		}
		raw := string(z.Raw())
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			token := z.Token()
			if token.Data == "img" {
				for i, attr := range token.Attr {
					if attr.Key != "src" {
						continue
					}
					rel, fragment, ok := htmlImagePath(document, attr.Val)
					ct := htmlImageType(rel)
					if !ok || ct == "" {
						continue
					}
					data, seen := cache[rel]
					if !seen && lookups < maxHTMLImageLookups {
						lookups++
						if content, exists := read(rel); exists && len(content) <= maxHTMLImageBytes && cached+base64.StdEncoding.EncodedLen(len(content))+64 <= maxHTMLImageOutput {
							data = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString([]byte(content))
							cached += len(data)
						}
						cache[rel] = data
					}
					if data == "" || len(body)+added+len(data)+len(fragment)+1 > maxHTMLImageOutput {
						continue
					}
					if fragment != "" {
						data += "#" + fragment
					}
					token.Attr[i].Val = data
					added += len(data)
					raw = token.String()
				}
			}
		}
		out.WriteString(raw)
	}
}
