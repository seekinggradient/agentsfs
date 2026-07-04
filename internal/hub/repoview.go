package hub

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// defaultRef is what the web space renders when no ref is given.
const defaultRef = "HEAD"

// RepoFile is one blob in a repo snapshot: its slash path, the one-line
// frontmatter description (for markdown), and the unix time of the most recent
// commit that touched it.
type RepoFile struct {
	Path        string
	Description string
	LastCommit  int64
}

// RepoSnapshot lists the files in a bare repo at ref, with descriptions and
// last-commit times, reading straight from git — no working tree, no checkout.
// A repo with no commits yet yields (nil, nil). Descriptions are parsed with
// core's frontmatter parser so the web view can never drift from the CLI.
func RepoSnapshot(gitPath, bareDir, ref string) ([]RepoFile, error) {
	if ref == "" {
		ref = defaultRef
	}
	// Empty repo (unborn ref): rev-parse fails; treat as no files.
	if err := exec.Command(gitPath, "-C", bareDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run(); err != nil {
		return nil, nil
	}
	out, err := exec.Command(gitPath, "-C", bareDir, "ls-tree", "-r", "--name-only", "-z", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("ls-tree %s: %w", ref, err)
	}
	last := lastCommitTimes(gitPath, bareDir, ref)

	var files []RepoFile
	for _, p := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if p == "" {
			continue
		}
		f := RepoFile{Path: p, LastCommit: last[p]}
		if strings.EqualFold(filepath.Ext(p), ".md") {
			f.Description = blobDescription(gitPath, bareDir, ref, p)
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// blobDescription reads a file's frontmatter description from the object store
// (git show <ref>:<path>) without checking anything out.
func blobDescription(gitPath, bareDir, ref, path string) string {
	out, err := exec.Command(gitPath, "-C", bareDir, "show", ref+":"+path).Output()
	if err != nil {
		return ""
	}
	return core.FrontmatterValueFromReader(bytes.NewReader(out), "description")
}

// lastCommitTimes maps each path to the unix time of the most recent commit
// touching it, in a single history pass (log is newest-first). Mirrors core's
// gitLastTouched but reads a bare repo at an explicit ref.
func lastCommitTimes(gitPath, bareDir, ref string) map[string]int64 {
	times := map[string]int64{}
	out, err := exec.Command(gitPath, "-C", bareDir, "-c", "core.quotepath=false",
		"log", "--format=\x01%ct", "--name-only", ref).Output()
	if err != nil {
		return times
	}
	var current int64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\x01") {
			if ts, err := strconv.ParseInt(line[1:], 10, 64); err == nil {
				current = ts
			}
			continue
		}
		if _, seen := times[line]; !seen {
			times[line] = current
		}
	}
	return times
}
