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

// headOID resolves ref to a commit id, or "" for an empty repo (unborn ref).
// It is the cheap per-request check the view cache keys on: a bare repo's
// content can only change by moving a ref.
func headOID(gitPath, bareDir, ref string) string {
	if ref == "" {
		ref = defaultRef
	}
	out, err := exec.Command(gitPath, "-C", bareDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// RepoSnapshot lists the files in a bare repo at ref, with descriptions and
// last-commit times, reading straight from git — no working tree, no checkout.
// A repo with no commits yet yields (nil, nil). Descriptions are parsed with
// core's frontmatter parser so the web view can never drift from the CLI.
func RepoSnapshot(gitPath, bareDir, ref string) ([]RepoFile, error) {
	oid := headOID(gitPath, bareDir, ref)
	if oid == "" {
		return nil, nil
	}
	entries, err := repoTreeEntries(gitPath, bareDir, oid)
	if err != nil {
		return nil, err
	}
	contents := markdownBlobContents(gitPath, bareDir, entries)
	return assembleFiles(gitPath, bareDir, oid, entries, contents, nil), nil
}

// assembleFiles joins tree entries, markdown contents, and last-commit times
// into the sorted RepoFile list every page renders from. prev (may be nil) is
// the previous view of this repo, letting the history walk cover only the
// commits since it instead of the repo's whole life.
func assembleFiles(gitPath, bareDir, oid string, entries []repoTreeEntry, contents map[string][]byte, prev *repoView) []RepoFile {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	last := lastCommitTimesFrom(gitPath, bareDir, oid, paths, prev)

	var files []RepoFile
	for _, e := range entries {
		p := e.Path
		f := RepoFile{Path: p, LastCommit: last[p]}
		if strings.EqualFold(filepath.Ext(p), ".md") {
			f.Description = core.FrontmatterValueFromReader(bytes.NewReader(contents[p]), "description")
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
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

// markdownBlobContents reads every markdown blob in one `git cat-file --batch`
// process. This keeps large knowledge bases responsive: the old path spawned
// one `git show` per markdown file on every repo and file page load.
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
	entries, err := repoTreeEntries(gitPath, bareDir, ref)
	if err != nil {
		return RepoGraph{Nodes: []RepoGraphNode{}, Links: []RepoGraphLink{}}
	}
	contents := markdownBlobContents(gitPath, bareDir, entries)
	return buildRepoGraphFrom(files, contents, user, repo)
}

// buildRepoGraphFrom is the pure part of BuildRepoGraph: it works entirely
// from an already-read snapshot, so the repo page can share one git read
// between the tree, the header, and the graph.
func buildRepoGraphFrom(files []RepoFile, contents map[string][]byte, user, repo string) RepoGraph {
	graph := RepoGraph{Nodes: []RepoGraphNode{}, Links: []RepoGraphLink{}}

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
	for _, f := range files {
		source, ok := idByPath[f.Path]
		if !ok {
			continue
		}
		data, ok := contents[f.Path]
		if !ok {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "[[") {
			continue
		}
		for _, l := range core.ScanLinksIn(f.Path, content) {
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

// lastCommitTimesFrom computes last-commit times for paths at oid. When prev
// captures an earlier commit of the same repo, only prev.OID..oid is walked
// and untouched paths carry their times over — so a push costs its own
// commits, not the repo's whole history. No usable prior view (or a
// force-push that dropped prev's commit) falls back to the full walk.
func lastCommitTimesFrom(gitPath, bareDir, oid string, paths []string, prev *repoView) map[string]int64 {
	if prev == nil || prev.OID == "" {
		return lastCommitTimes(gitPath, bareDir, oid, paths)
	}
	if exec.Command(gitPath, "-C", bareDir, "merge-base", "--is-ancestor", prev.OID, oid).Run() != nil {
		return lastCommitTimes(gitPath, bareDir, oid, paths)
	}
	touched := lastCommitTimes(gitPath, bareDir, prev.OID+".."+oid, nil)
	prevTimes := make(map[string]int64, len(prev.Files))
	for _, f := range prev.Files {
		prevTimes[f.Path] = f.LastCommit
	}
	times := make(map[string]int64, len(paths))
	for _, p := range paths {
		if t, ok := touched[p]; ok {
			times[p] = t
		} else {
			times[p] = prevTimes[p]
		}
	}
	return times
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

// BlobSize returns the size of a file at ref without reading its contents.
// Keeping this separate from BlobContent lets web views reject or preview
// large blobs without first buffering them into the hub process.
func BlobSize(gitPath, bareDir, ref, filePath string) (int64, bool) {
	out, err := exec.Command(gitPath, "-C", bareDir, "cat-file", "-s", ref+":"+filePath).Output()
	if err != nil {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

// BlobContent returns the content of a file at ref (git show ref:path).
func BlobContent(gitPath, bareDir, ref, path string) (string, bool) {
	out, err := exec.Command(gitPath, "-C", bareDir, "show", ref+":"+path).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// StreamBlob writes a blob directly from git to dst. It is the raw-file
// escape hatch for media and large files, where buffering the entire object
// would make an otherwise harmless browser click expensive or fragile.
func StreamBlob(gitPath, bareDir, ref, filePath string, dst io.Writer) error {
	cmd := exec.Command(gitPath, "-C", bareDir, "cat-file", "blob", ref+":"+filePath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, stdout)
	waitErr := cmd.Wait()
	if copyErr != nil {
		return copyErr
	}
	return waitErr
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

// CommitDiff returns the unified patch introduced by hash. The caller should
// first establish that hash is a commit the viewer is allowed to inspect.
// Using git show keeps this working for both root commits and normal commits.
func CommitDiff(gitPath, bareDir, hash string) (string, bool) {
	if strings.TrimSpace(hash) == "" {
		return "", false
	}
	cmd := exec.Command(gitPath, "-C", bareDir, "show", "--format=", "--patch", "--no-ext-diff", "--no-renames", "--no-color", "--unified=3", hash, "--")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// graphBacklinks returns the notes that link to targetPath, straight from the
// prebuilt wikilink graph — the file page does no extra git reads for its
// backlinks panel. Sources come back in path order (node ids are assigned in
// sorted-path order and links are sorted by source).
func graphBacklinks(graph RepoGraph, targetPath string) []RepoGraphNode {
	target := -1
	for _, n := range graph.Nodes {
		if n.Path == targetPath {
			target = n.ID
			break
		}
	}
	if target < 0 {
		return nil
	}
	var res []RepoGraphNode
	for _, l := range graph.Links {
		if l.Target == target {
			res = append(res, graph.Nodes[l.Source])
		}
	}
	return res
}
