package hub

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
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

type RepoGraphNode struct {
	ID     int    `json:"id"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Desc   string `json:"desc,omitempty"`
	Href   string `json:"href"`
	Group  string `json:"group,omitempty"`
	Degree int    `json:"degree"`
}

type RepoGraphLink struct {
	Source int `json:"source"`
	Target int `json:"target"`
	Count  int `json:"count"`
}

type RepoGraph struct {
	Nodes []RepoGraphNode `json:"nodes"`
	Links []RepoGraphLink `json:"links"`
}

type repoTreeEntry struct {
	Path string
	OID  string
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
	entries, err := repoTreeEntries(gitPath, bareDir, ref)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	last := lastCommitTimes(gitPath, bareDir, ref, paths)
	descs := blobDescriptions(gitPath, bareDir, entries)

	var files []RepoFile
	for _, e := range entries {
		p := e.Path
		f := RepoFile{Path: p, LastCommit: last[p]}
		if strings.EqualFold(filepath.Ext(p), ".md") {
			f.Description = descs[p]
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// repoTreeEntries lists current blobs with their object ids. Keeping the OID
// lets blobDescriptions batch-read the markdown files instead of spawning one
// `git show` per note.
func repoTreeEntries(gitPath, bareDir, ref string) ([]repoTreeEntry, error) {
	out, err := exec.Command(gitPath, "-C", bareDir, "ls-tree", "-r", "-z", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("ls-tree %s: %w", ref, err)
	}
	var entries []repoTreeEntry
	for _, rec := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 || tab == len(rec)-1 {
			continue
		}
		fields := strings.Fields(string(rec[:tab]))
		if len(fields) < 3 || fields[1] != "blob" {
			continue
		}
		entries = append(entries, repoTreeEntry{OID: fields[2], Path: string(rec[tab+1:])})
	}
	return entries, nil
}

// blobDescriptions reads every markdown blob's frontmatter in one git process.
// This keeps large knowledge bases responsive: the old path spawned one
// `git show` per markdown file on every repo and file page load.
func blobDescriptions(gitPath, bareDir string, entries []repoTreeEntry) map[string]string {
	descs := map[string]string{}
	for p, data := range markdownBlobContents(gitPath, bareDir, entries) {
		descs[p] = core.FrontmatterValueFromReader(bytes.NewReader(data), "description")
	}
	return descs
}

func markdownBlobContents(gitPath, bareDir string, entries []repoTreeEntry) map[string][]byte {
	contents := map[string][]byte{}
	var input bytes.Buffer
	var paths []string
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Path), ".md") {
			continue
		}
		paths = append(paths, e.Path)
		input.WriteString(e.OID)
		input.WriteByte('\n')
	}
	if len(paths) == 0 {
		return contents
	}
	cmd := exec.Command(gitPath, "-C", bareDir, "cat-file", "--batch")
	cmd.Stdin = &input
	out, err := cmd.Output()
	if err != nil {
		return contents
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			return contents
		}
		fields := strings.Fields(strings.TrimSpace(header))
		if len(fields) < 3 || fields[1] != "blob" {
			return contents
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return contents
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return contents
		}
		// cat-file separates batch records with a newline after the object body.
		_, _ = r.ReadByte()
		contents[p] = data
	}
	return contents
}

// BuildRepoGraph scans markdown wikilinks and returns a compact graph for the
// repo page. Nodes are markdown files; edges are resolved [[wikilinks]] between
// them, using the same suffix-style matching as core.NameIndex.
func BuildRepoGraph(gitPath, bareDir, ref, user, repo string, files []RepoFile) RepoGraph {
	graph := RepoGraph{Nodes: []RepoGraphNode{}, Links: []RepoGraphLink{}}
	if ref == "" {
		ref = defaultRef
	}
	entries, err := repoTreeEntries(gitPath, bareDir, ref)
	if err != nil {
		return graph
	}
	contents := markdownBlobContents(gitPath, bareDir, entries)

	idByPath := map[string]int{}
	resolver := map[string][]int{}
	for _, f := range files {
		if !strings.EqualFold(filepath.Ext(f.Path), ".md") {
			continue
		}
		id := len(graph.Nodes)
		idByPath[f.Path] = id
		linkable := linkableName(f.Path)
		for _, key := range suffixKeys(linkable) {
			resolver[key] = append(resolver[key], id)
		}
		graph.Nodes = append(graph.Nodes, RepoGraphNode{
			ID:    id,
			Path:  f.Path,
			Name:  graphNodeName(f.Path),
			Desc:  cleanDesc(f.Description),
			Href:  "/" + user + "/" + repo + "/blob/" + f.Path,
			Group: graphGroup(f.Path),
		})
	}

	edgeCounts := map[[2]int]int{}
	for _, e := range entries {
		source, ok := idByPath[e.Path]
		if !ok {
			continue
		}
		data, ok := contents[e.Path]
		if !ok {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "[[") {
			continue
		}
		for _, l := range core.ScanLinksIn(e.Path, content) {
			for _, target := range resolver[strings.ToLower(strings.TrimSpace(l.Target))] {
				if target == source {
					continue
				}
				edgeCounts[[2]int{source, target}]++
			}
		}
	}

	for edge, count := range edgeCounts {
		graph.Nodes[edge[0]].Degree += count
		graph.Nodes[edge[1]].Degree += count
		graph.Links = append(graph.Links, RepoGraphLink{Source: edge[0], Target: edge[1], Count: count})
	}
	sort.Slice(graph.Links, func(i, j int) bool {
		a, b := graph.Links[i], graph.Links[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Target < b.Target
	})
	return graph
}

func linkableName(rel string) string {
	if strings.EqualFold(filepath.Ext(rel), ".md") {
		rel = rel[:len(rel)-len(filepath.Ext(rel))]
	}
	return strings.ToLower(rel)
}

func suffixKeys(linkable string) []string {
	parts := strings.Split(linkable, "/")
	keys := make([]string, 0, len(parts))
	for i := range parts {
		keys = append(keys, strings.Join(parts[i:], "/"))
	}
	return keys
}

func graphNodeName(rel string) string {
	name := rel
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".md") {
		name = name[:len(name)-len(ext)]
	}
	return name
}

func graphGroup(rel string) string {
	if i := strings.Index(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "root"
}

// lastCommitTimes maps each path to the unix time of the most recent commit
// touching it, in a single newest-first history pass. It streams output and
// stops as soon as every current file has been seen, avoiding a full-history
// read for repos where the latest commit touches most files.
func lastCommitTimes(gitPath, bareDir, ref string, paths []string) map[string]int64 {
	times := map[string]int64{}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	cmd := exec.Command(gitPath, "-C", bareDir, "-c", "core.quotepath=false",
		"log", "--format=\x01%ct", "--name-only", ref)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return times
	}
	if err := cmd.Start(); err != nil {
		return times
	}
	doneEarly := false
	var current int64
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\x01") {
			if ts, err := strconv.ParseInt(line[1:], 10, 64); err == nil {
				current = ts
			}
			continue
		}
		if len(want) > 0 && !want[line] {
			continue
		}
		if _, seen := times[line]; !seen {
			times[line] = current
			if len(want) > 0 && len(times) == len(want) {
				doneEarly = true
				_ = cmd.Process.Kill()
				break
			}
		}
	}
	if err := cmd.Wait(); err != nil && !doneEarly {
		return times
	}
	return times
}

// BlobContent returns the content of a file at ref (git show ref:path).
func BlobContent(gitPath, bareDir, ref, path string) (string, bool) {
	out, err := exec.Command(gitPath, "-C", bareDir, "show", ref+":"+path).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// Commit is one entry in a repo's history.
type Commit struct {
	Hash    string
	Short   string
	Subject string
	Author  string
	When    int64
}

// RepoLog returns up to limit commits (newest first) for ref.
func RepoLog(gitPath, bareDir, ref string, limit int) []Commit {
	return RepoLogPath(gitPath, bareDir, ref, "", limit)
}

// RepoLogPath returns up to limit commits (newest first) for ref, optionally
// restricted to commits that touched filePath.
func RepoLogPath(gitPath, bareDir, ref, filePath string, limit int) []Commit {
	if limit <= 0 {
		limit = 50
	}
	args := []string{"-C", bareDir, "log", "--format=%H%x1f%h%x1f%s%x1f%an%x1f%ct", "-n", strconv.Itoa(limit), ref}
	if filePath != "" {
		args = append(args, "--", filePath)
	}
	out, err := exec.Command(gitPath, args...).Output()
	if err != nil {
		return nil
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(f[4], 10, 64)
		commits = append(commits, Commit{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], When: ts})
	}
	return commits
}

// RepoBacklinks returns the links across the repo that resolve to targetPath.
// It batch-reads markdown files, then applies core's exact link scan and the
// same suffix-based name resolution as core.NameIndex for this one target.
func RepoBacklinks(gitPath, bareDir, ref, targetPath string, _ *core.NameIndex) []core.Link {
	entries, err := repoTreeEntries(gitPath, bareDir, ref)
	if err != nil {
		return nil
	}
	contents := markdownBlobContents(gitPath, bareDir, entries)
	var res []core.Link
	for _, e := range entries {
		path := e.Path
		if path == targetPath {
			continue
		}
		data, ok := contents[path]
		if !ok {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "[[") {
			continue
		}
		for _, l := range core.ScanLinksIn(path, content) {
			if linkTargetMatchesPath(l.Target, targetPath) {
				res = append(res, l)
			}
		}
	}
	return res
}

func linkTargetMatchesPath(target, filePath string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	if t == "" {
		return false
	}
	linkable := strings.ToLower(filePath)
	if strings.EqualFold(filepath.Ext(linkable), ".md") {
		linkable = linkable[:len(linkable)-len(filepath.Ext(linkable))]
	}
	return linkable == t || strings.HasSuffix(linkable, "/"+t)
}
