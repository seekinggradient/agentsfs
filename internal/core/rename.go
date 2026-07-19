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
	oldRel, err := resolveExisting(root, oldArg)
	if err != nil {
		return nil, err
	}
	if !fileExists(joinRel(root, oldRel)) {
		return nil, fmt.Errorf("%s does not exist (paths are relative to the instance root %s)", oldRel, root)
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
	// Collect the links to rewrite, remembering WHICH text resolved to the file.
	// [[Note#1]] may resolve either because a file is literally named "Note#1.md"
	// or because "Note.md" exists and #1 is a heading; the rewrite differs, so the
	// matched key travels with the link rather than being re-derived later.
	type pendingLink struct {
		link Link
		key  string
	}
	resolvesToOld := func(target string) bool {
		for _, m := range idx.Resolve(target) {
			if m == oldRel {
				return true
			}
		}
		return false
	}
	bySource := map[string][]pendingLink{}
	for _, l := range links {
		if isRootContract(l.Source) {
			continue
		}
		key := ""
		switch {
		case l.Anchor != "" && resolvesToOld(l.Written()):
			key = l.Written()
		case resolvesToOld(l.Target):
			key = l.Target
		default:
			continue
		}
		bySource[l.Source] = append(bySource[l.Source], pendingLink{l, key})
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
		// Rewrite only the lines the scanner found links on, masking inline
		// code — so quoted [[links]] in fences and backticks survive, with
		// exactly the semantics ScanLinks applies when reading.
		lines := strings.Split(string(data), "\n")
		for _, p := range ls {
			if p.link.Line < 1 || p.link.Line > len(lines) {
				continue
			}
			rewritten, n := rewriteLinkOutsideCode(lines[p.link.Line-1], p.key, newTarget)
			lines[p.link.Line-1] = rewritten
			res.LinksRewrote += n
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return nil, err
		}
		res.FilesChanged = append(res.FilesChanged, source)
	}
	return res, nil
}

// rewriteLinkOutsideCode retargets every [[…]] whose target is oldTarget in
// the parts of line not inside inline code spans, preserving each link's
// #anchor and |alias, and returns the number of replacements performed.
//
// It re-parses each link with parseLinkInner rather than matching the literal
// "[[oldTarget]]" text, because the target a link resolves by is only part of
// what is written: [[Note#Section|alias]] resolves by "Note". Matching literal
// text would silently skip anchored links — leaving them pointing at the old
// name while rename reported success.
func rewriteLinkOutsideCode(line, oldTarget, newTarget string) (string, int) {
	count := 0
	replace := func(seg string) string {
		return linkRe.ReplaceAllStringFunc(seg, func(m string) string {
			target, anchor, alias := parseLinkInner(m[2 : len(m)-2])
			written := target
			if anchor != "" {
				written = target + "#" + anchor
			}
			switch oldTarget {
			case written:
				// The whole written form was the file's name (a file literally
				// called "Note#1.md"), so the '#' part is not a heading to keep.
				count++
				return formatLink(newTarget, "", alias)
			case target:
				count++
				return formatLink(newTarget, anchor, alias)
			}
			return m
		})
	}
	var b strings.Builder
	last := 0
	for _, span := range inlineCodeRe.FindAllStringIndex(line, -1) {
		b.WriteString(replace(line[last:span[0]]))
		b.WriteString(line[span[0]:span[1]]) // quoted code: untouched
		last = span[1]
	}
	b.WriteString(replace(line[last:]))
	return b.String(), count
}

// resolveExisting interprets a path argument the way a human means it:
// root-relative (the canonical convention), or cwd-relative as a
// convenience when that file actually exists inside the instance.
func resolveExisting(root, arg string) (string, error) {
	if filepath.IsAbs(arg) {
		return toRel(root, arg)
	}
	rootRel := filepath.ToSlash(filepath.Clean(arg))
	if fileExists(joinRel(root, rootRel)) {
		return rootRel, nil
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := toRel(root, filepath.Join(cwd, arg)); err == nil && fileExists(joinRel(root, rel)) {
			return rel, nil
		}
	}
	return rootRel, nil // not found either way; caller reports against the canonical form
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
