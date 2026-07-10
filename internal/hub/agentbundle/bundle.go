// Package agentbundle builds and validates the agentsfs-chat source archive
// embedded in the Hub. The archive is intentionally an allowlist: local env
// files, repository metadata, tests, docs, dependencies, and editor artifacts
// can never be copied into a Sprite by accident.
package agentbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxFileSize  = 5 << 20
	maxTotalSize = 20 << 20
)

var (
	allowedSourceExtensions = map[string]bool{
		".css":  true,
		".html": true,
		".js":   true,
		".json": true,
		".ts":   true,
	}
	requiredFiles = []string{
		"package-lock.json",
		"package.json",
		"tsconfig.json",
		"src/index.ts",
		"src/server/server.ts",
		"src/web/app.js",
		"src/web/index.html",
		"src/web/lib.js",
		"src/web/styles.css",
	}
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
		regexp.MustCompile(`(?m)\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`(?m)\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`(?m)\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`(?mi)\b(?:OPENAI|TAVILY|FMP|PEXELS)_API_KEY\s*=\s*["']?[A-Za-z0-9_./+:-]{8,}`),
	}
)

type sourceFile struct {
	name string
	data []byte
}

// AllowedPath reports whether name is part of the Sprite runtime allowlist.
func AllowedPath(name string) bool {
	if name == "package.json" || name == "package-lock.json" || name == "tsconfig.json" {
		return true
	}
	if !strings.HasPrefix(name, "src/") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return allowedSourceExtensions[path.Ext(name)]
}

// Build writes a deterministic, secret-scanned tar.gz archive from sourceDir.
// Only package*.json and runtime files below src/ are candidates for inclusion.
func Build(sourceDir, outputPath string) error {
	files, err := collect(sourceDir)
	if err != nil {
		return err
	}

	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	gz.Header.ModTime = time.Time{}
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)
	epoch := time.Unix(0, 0).UTC()
	for _, file := range files {
		hdr := &tar.Header{
			Name:     file.name,
			Mode:     0o644,
			Size:     int64(len(file.data)),
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write %s header: %w", file.name, err)
		}
		if _, err := tw.Write(file.data); err != nil {
			return fmt.Errorf("write %s: %w", file.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := Validate(bytes.NewReader(archive.Bytes())); err != nil {
		return fmt.Errorf("validate generated bundle: %w", err)
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".agent-bundle-*.tgz")
	if err != nil {
		return fmt.Errorf("create temporary bundle: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set bundle permissions: %w", err)
	}
	if _, err := tmp.Write(archive.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("write bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close bundle: %w", err)
	}
	if err := os.Rename(tmpName, outputPath); err != nil {
		return fmt.Errorf("replace bundle: %w", err)
	}
	return nil
}

func collect(sourceDir string) ([]sourceFile, error) {
	var files []sourceFile
	var total int64
	add := func(name, filename string) error {
		info, err := os.Lstat(filename)
		if err != nil {
			return fmt.Errorf("inspect required file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", name)
		}
		if info.Size() > maxFileSize {
			return fmt.Errorf("%s exceeds the per-file size limit", name)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if containsSecret(data) {
			return fmt.Errorf("%s contains a credential-like value", name)
		}
		total += int64(len(data))
		if total > maxTotalSize {
			return errors.New("bundle exceeds the total size limit")
		}
		files = append(files, sourceFile{name: name, data: data})
		return nil
	}

	for _, name := range []string{"package-lock.json", "package.json", "tsconfig.json"} {
		if err := add(name, filepath.Join(sourceDir, filepath.FromSlash(name))); err != nil {
			return nil, err
		}
	}
	srcDir := filepath.Join(sourceDir, "src")
	if err := filepath.WalkDir(srcDir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if entry.IsDir() {
			if name != "src" && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !AllowedPath(name) {
			return nil
		}
		return add(name, filename)
	}); err != nil {
		return nil, fmt.Errorf("walk runtime source: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

// Validate checks a bundle using the same rules enforced by Build. Hub tests
// run this against the tracked go:embed archive, so manually replacing the
// archive with a broad tarball fails CI before it can be deployed.
func Validate(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	if !gz.Header.ModTime.IsZero() || gz.Header.Name != "" || gz.Header.Comment != "" {
		return errors.New("gzip metadata is not deterministic")
	}

	tr := tar.NewReader(gz)
	seen := make(map[string]bool)
	last := ""
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		name := hdr.Name
		if !AllowedPath(name) {
			return fmt.Errorf("archive contains disallowed path %q", name)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return fmt.Errorf("archive entry %q is not a regular file", name)
		}
		if hdr.Mode != 0o644 || hdr.Uid != 0 || hdr.Gid != 0 || !hdr.ModTime.Equal(time.Unix(0, 0)) {
			return fmt.Errorf("archive entry %q has non-deterministic metadata", name)
		}
		if seen[name] {
			return fmt.Errorf("archive contains duplicate path %q", name)
		}
		if last != "" && name < last {
			return errors.New("archive paths are not sorted")
		}
		seen[name] = true
		last = name
		if hdr.Size < 0 || hdr.Size > maxFileSize {
			return fmt.Errorf("archive entry %q exceeds the size limit", name)
		}
		total += hdr.Size
		if total > maxTotalSize {
			return errors.New("archive exceeds the total size limit")
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxFileSize+1))
		if err != nil {
			return fmt.Errorf("read archive entry %q: %w", name, err)
		}
		if int64(len(data)) != hdr.Size {
			return fmt.Errorf("archive entry %q has an invalid size", name)
		}
		if containsSecret(data) {
			return fmt.Errorf("archive entry %q contains a credential-like value", name)
		}
	}
	for _, name := range requiredFiles {
		if !seen[name] {
			return fmt.Errorf("archive is missing required runtime file %q", name)
		}
	}
	return nil
}

func containsSecret(data []byte) bool {
	for _, pattern := range secretPatterns {
		if pattern.Match(data) {
			return true
		}
	}
	return false
}
