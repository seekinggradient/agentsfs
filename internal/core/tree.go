package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tree renders the instance — or a subtree of it — as an indented tree with
// each entry's one-line description and git last-touched age: the
// progressive-disclosure view an agent orients from in a single call.
// INDEX.md files are not listed; the directory line carries their
// description.
//
// subdir scopes the view to one directory, slash-relative to root ("." or ""
// means the whole instance). maxDepth caps how many levels below the starting
// directory are shown; 0 means unlimited. A directory whose children are
// hidden by the depth cap is flagged with " …" so the agent knows to look
// deeper.
func Tree(root, subdir string, maxDepth int) (string, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return "", err
	}
	scope := normalizeScope(subdir)
	if scope != "." {
		isDir := false
		for _, e := range entries {
			if e.IsDir && e.Rel == scope {
				isDir = true
				break
			}
		}
		if !isDir {
			return "", fmt.Errorf("no such directory in instance: %s", scope)
		}
	}
	touched := gitLastTouched(root)

	var b strings.Builder
	rootLabel, rootDesc := ".", DirDescription(root, ".")
	if scope != "." {
		rootLabel, rootDesc = scope, DirDescription(root, scope)
	}
	fmt.Fprintf(&b, "%s%s\n", rootLabel, annotate(rootDesc, touched[scope]))

	children := map[string][]Entry{}
	for _, e := range entries {
		if !e.IsDir && strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue
		}
		children[parentOf(e.Rel)] = append(children[parentOf(e.Rel)], e)
	}

	var walk func(dir, prefix string, depth int)
	walk = func(dir, prefix string, depth int) {
		kids := children[dir]
		sort.Slice(kids, func(i, j int) bool {
			if kids[i].IsDir != kids[j].IsDir {
				return !kids[i].IsDir // files before subdirectories
			}
			return kids[i].Rel < kids[j].Rel
		})
		for i, e := range kids {
			conn, childPrefix := "├── ", prefix+"│   "
			if i == len(kids)-1 {
				conn, childPrefix = "└── ", prefix+"    "
			}
			name := baseName(e.Rel)
			if e.IsDir {
				desc := DirDescription(root, e.Rel)
				atLimit := maxDepth > 0 && depth >= maxDepth
				more := ""
				if atLimit && len(children[e.Rel]) > 0 {
					more = " …"
				}
				fmt.Fprintf(&b, "%s%s%s/%s%s\n", prefix, conn, name, annotate(desc, touched[e.Rel]), more)
				if !atLimit {
					walk(e.Rel, childPrefix, depth+1)
				}
			} else {
				desc := ""
				if isMarkdown(e.Rel) {
					desc = Description(joinRel(root, e.Rel))
				}
				fmt.Fprintf(&b, "%s%s%s%s\n", prefix, conn, name, annotate(desc, touched[e.Rel]))
			}
		}
	}
	walk(scope, "", 1)
	return b.String(), nil
}

// normalizeScope reduces a caller-supplied subdirectory to the slash-relative
// form Tree keys on, mapping the root ("", ".", "/") to ".".
func normalizeScope(subdir string) string {
	s := strings.Trim(strings.ReplaceAll(subdir, "\\", "/"), "/")
	if s == "" || s == "." {
		return "."
	}
	return s
}

func annotate(desc, age string) string {
	s := ""
	if desc != "" {
		s += " — " + desc
	}
	if age != "" {
		s += "  [" + age + "]"
	}
	return s
}

func parentOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "."
}

func baseName(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// gitLastTouched returns rel path → human age ("3d ago") from a single
// history pass. Directories get the age of their most recently touched
// file. Files git doesn't know yet map to "uncommitted".
func gitLastTouched(root string) map[string]string {
	ages := map[string]string{}
	// --relative: an instance nested in a larger repo still gets paths
	// relative to the instance root.
	out, err := git(root, "-c", "core.quotepath=false", "log", "--format=\x01%ct", "--name-only", "--relative", "--", ".")
	if err != nil {
		return ages
	}
	times := map[string]int64{}
	var current int64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\x01") {
			if ts, err := strconv.ParseInt(line[1:], 10, 64); err == nil {
				current = ts
			}
			continue
		}
		if _, seen := times[line]; !seen { // log is newest-first
			times[line] = current
		}
	}
	now := time.Now()
	for path, ts := range times {
		ages[path] = humanAge(now.Sub(time.Unix(ts, 0)))
		// Propagate to ancestor dirs: a dir is as fresh as its freshest file.
		for dir := parentOf(path); dir != "."; dir = parentOf(dir) {
			if cur, ok := times[dir+"/"]; !ok || ts > cur {
				times[dir+"/"] = ts
				ages[dir] = humanAge(now.Sub(time.Unix(ts, 0)))
			}
		}
	}
	// Anything on disk but absent from history is uncommitted.
	if entries, err := ListEntries(root); err == nil {
		for _, e := range entries {
			if !e.IsDir {
				if _, ok := times[e.Rel]; !ok {
					ages[e.Rel] = "uncommitted"
				}
			}
		}
	}
	return ages
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "<1h ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}
