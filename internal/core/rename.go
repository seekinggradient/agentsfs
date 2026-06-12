package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RenameResult reports what Rename touched, so callers can narrate and
// commit with an accurate message.
type RenameResult struct {
	OldRel       string
	NewRel       string
	LinksRewrote int
	FilesChanged []string
}

// Rename moves a file and rewrites every [[wikilink]] that resolves to it —
// the LSP "rename symbol" refactor applied to knowledge. Nothing is
// committed; the calling agent reviews and commits with its own message.
func Rename(root, oldArg, newArg string) (*RenameResult, error) {
	oldRel, err := toRel(root, oldArg)
	if err != nil {
		return nil, err
	}
	if !fileExists(joinRel(root, oldRel)) {
		return nil, fmt.Errorf("%s does not exist", oldRel)
	}

	newRel, err := toRel(root, newArg)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(newArg, "/") && !strings.Contains(newArg, string(os.PathSeparator)) {
		newRel = filepath.ToSlash(filepath.Join(parentOf(oldRel), baseName(newRel)))
	}
	if filepath.Ext(newRel) == "" {
		newRel += filepath.Ext(oldRel)
	}
	if fileExists(joinRel(root, newRel)) {
		return nil, fmt.Errorf("%s already exists", newRel)
	}

	// Find every link that resolves to the old file before moving it.
	links, err := ScanLinks(root)
	if err != nil {
		return nil, err
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		return nil, err
	}
	bySource := map[string][]Link{}
	for _, l := range links {
		if isRootContract(l.Source) {
			continue
		}
		for _, m := range idx.Resolve(l.Target) {
			if m == oldRel {
				bySource[l.Source] = append(bySource[l.Source], l)
				break
			}
		}
	}

	// Move the file (git mv keeps history legible when available).
	if err := os.MkdirAll(filepath.Dir(joinRel(root, newRel)), 0o755); err != nil {
		return nil, err
	}
	if _, gitErr := git(root, "mv", filepath.FromSlash(oldRel), filepath.FromSlash(newRel)); gitErr != nil {
		if err := os.Rename(joinRel(root, oldRel), joinRel(root, newRel)); err != nil {
			return nil, err
		}
	}

	// Rewrite link targets. The new target is the new name in its simplest
	// form; if that's ambiguous, doctor will flag it for disambiguation.
	newTarget := baseName(newRel)
	if isMarkdown(newRel) {
		newTarget = strings.TrimSuffix(newTarget, filepath.Ext(newTarget))
	}
	res := &RenameResult{OldRel: oldRel, NewRel: newRel}
	for source, ls := range bySource {
		path := joinRel(root, source)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		for _, l := range ls {
			content = rewriteLink(content, l.Target, newTarget)
			res.LinksRewrote++
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, err
		}
		res.FilesChanged = append(res.FilesChanged, source)
	}
	return res, nil
}

// rewriteLink replaces [[oldTarget]] and [[oldTarget|alias]] occurrences,
// preserving aliases.
func rewriteLink(content, oldTarget, newTarget string) string {
	content = strings.ReplaceAll(content, "[["+oldTarget+"]]", "[["+newTarget+"]]")
	content = strings.ReplaceAll(content, "[["+oldTarget+"|", "[["+newTarget+"|")
	return content
}

func toRel(root, arg string) (string, error) {
	if filepath.IsAbs(arg) {
		rel, err := filepath.Rel(root, arg)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("%s is outside the instance at %s", arg, root)
		}
		return filepath.ToSlash(rel), nil
	}
	return filepath.ToSlash(filepath.Clean(arg)), nil
}
