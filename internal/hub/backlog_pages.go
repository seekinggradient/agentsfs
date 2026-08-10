package hub

import (
	"path"
	"regexp"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// Backlog directories — which page inside one a request is looking at.
//
// Contract 0.11.0 turned the backlog into a DIRECTORY role (see
// agentsfs/rfcs/backlog-directories.md): the marker lives in the directory's
// INDEX.md, and that INDEX.md is itself the spine — the task page. Around it
// sit ticket detail files, delegated sub-backlog subdirectories with spines of
// their own, and an `archive/` collection holding what closed.
//
// A page therefore renders one of four ways, and only the first is decided by
// the page's own frontmatter:
//
//   - the spine (a marked INDEX.md, or a legacy 0.10.0 page carrying the
//     marker) and delegated sub-spines: the task rendering;
//   - a ticket: ordinary markdown plus the `## Log` timeline;
//   - an archived page: the same, with a muted chip saying when it closed;
//   - an archive rollup (`archive/<year>.md`): the task rendering, read-only,
//     for the terminal markers its lines carry.
//
// Everything but the first needs the ancestor INDEX.md's frontmatter, read from
// the git tree at render time exactly the way isCollectionDir reads a
// collection's. Nothing here writes, and nothing here fails a render: a lookup
// that cannot be answered simply leaves the page rendering as ordinary markdown.

const (
	// backlogArchiveDirName is the archive collection inside a backlog
	// directory. Like core's, it is a name rather than a marker: the gardener
	// creates it lazily, and it must be excluded before any INDEX.md inside it
	// can be mistaken for a sub-backlog's spine.
	backlogArchiveDirName = "archive"
	// backlogIndexName is the spine's file name — the directory's own
	// self-description and its task page in one file.
	backlogIndexName = "INDEX.md"
	// maxBacklogAncestors bounds the walk up from a page to the backlog
	// directory that might contain it. Backlog nesting is spine → sub-backlog →
	// archive; this leaves room to spare and keeps a pathological tree from
	// turning one page view into an unbounded number of blob reads.
	maxBacklogAncestors = 8
)

// backlogRollupRe matches an archive rollup page: one file per closed year.
var backlogRollupRe = regexp.MustCompile(`^\d{4}$`)

// backlogPlacement is how one file relates to the backlog directory around it.
// The
// zero value — an ordinary note, nowhere near a backlog — is the common case.
type backlogPlacement struct {
	spine    bool   // the task page: a marked INDEX.md, a sub-spine, or a legacy page
	ticket   bool   // a detail file beside a spine
	archived bool   // inside <root>/archive/
	rollup   bool   // archive/<year>.md
	dir      string // the directory this page's sibling links resolve against
	root     string // the backlog directory declaring the role; "" for a legacy page
}

// inBacklog reports whether the page is part of a backlog at all.
func (p backlogPlacement) inBacklog() bool { return p.spine || p.ticket || p.archived }

// resolveBacklogPlacement classifies filePath within its instance. content is the
// file's own bytes (its frontmatter decides the spine case without any lookup);
// blob reads another file in the same revision, reporting whether it exists.
func resolveBacklogPlacement(filePath, content string, blob func(relPath string) (string, bool)) backlogPlacement {
	if !strings.EqualFold(path.Ext(filePath), ".md") {
		return backlogPlacement{}
	}
	// The page's own marker, read with core's parser and constant so the Hub
	// and the CLI can never disagree about which page is the backlog.
	if isBacklogRole(core.FrontmatterValueFromReader(strings.NewReader(content), "agentsfs_role")) {
		if strings.EqualFold(pathBase(filePath), backlogIndexName) {
			dir := path.Dir(filePath)
			return backlogPlacement{spine: true, dir: dir, root: dir}
		}
		// A page-level marker is 0.10.0's retired shape. It still renders as the
		// spine — read-only, as it always was — with no directory around it.
		return backlogPlacement{spine: true}
	}

	root, ok := backlogRootFor(filePath, blob)
	if !ok {
		return backlogPlacement{}
	}
	page := backlogPlacement{dir: path.Dir(filePath), root: root}
	rel := strings.TrimPrefix(filePath, root+"/")
	base := pathBase(filePath)
	switch {
	case strings.HasPrefix(rel, backlogArchiveDirName+"/"):
		page.archived = true
		// A rollup is the archive's per-year page of one-liner tasks: terminal
		// markers, no bands. The archive's own INDEX.md is the collection's
		// descriptor and is neither.
		if name := strings.TrimSuffix(base, path.Ext(base)); backlogRollupRe.MatchString(name) {
			page.rollup = true
		} else if !strings.EqualFold(base, backlogIndexName) {
			page.ticket = true
		}
	case strings.EqualFold(base, backlogIndexName):
		// A sub-backlog's spine. It carries no marker of its own — being a
		// subdirectory of the backlog directory is what makes it one — so its
		// task grammar would otherwise render as bare GFM.
		page.spine = true
	default:
		page.ticket = true
	}
	return page
}

// backlogRootFor walks up from a page looking for the directory whose INDEX.md
// declares the backlog role, mirroring isCollectionDir's ancestor lookup. It
// reads at most one short blob per level and stops at the instance root.
func backlogRootFor(filePath string, blob func(string) (string, bool)) (string, bool) {
	if blob == nil {
		return "", false
	}
	dir := path.Dir(filePath)
	for i := 0; i < maxBacklogAncestors; i++ {
		if dir == "" || dir == "." || dir == "/" {
			return "", false
		}
		if content, ok := blob(dir + "/" + backlogIndexName); ok &&
			isBacklogRole(core.FrontmatterValueFromReader(strings.NewReader(content), "agentsfs_role")) {
			return dir, true
		}
		dir = path.Dir(dir)
	}
	return "", false
}

// backlogChipView is the header chip a ticket or archived page carries: the
// backlog it belongs to, and whether it has closed. A ticket is easy to open
// from a search result with no idea what it is part of, and an archived one
// reads exactly like a live one — the chip is what says otherwise.
type backlogChipView struct {
	SpineHref  string
	SpineLabel string
	Archived   bool
	Closed     string // the `closed:` fact-date the archive sweep stamped
}

// backlogChipFor builds that chip. content is the page's own bytes, read for
// the `closed:` frontmatter the sweep stamps when it moves a ticket in.
func backlogChipFor(page backlogPlacement, user, repo, content string) *backlogChipView {
	if !page.inBacklog() || page.root == "" {
		return nil
	}
	chip := &backlogChipView{
		SpineHref:  "/" + user + "/" + repo + "/blob/" + page.root + "/" + backlogIndexName,
		SpineLabel: pathBase(page.root),
		Archived:   page.archived,
	}
	if page.archived {
		chip.Closed = backlogClosedDate(content)
	}
	return chip
}

// backlogClosedRe finds the `closed: YYYY-MM-DD` stamp the archive sweep writes
// on a ticket as it moves it in. It is anchored to the start of a frontmatter
// line and accepts nothing but a bare ISO date.
var backlogClosedRe = regexp.MustCompile(`(?m)^closed:[ \t]*"?(\d{4}-\d{2}-\d{2})"?[ \t]*$`)

// backlogClosedDate reads that stamp out of a page's frontmatter.
//
// This is the one frontmatter value the Hub reads without core's parser, for a
// reason worth stating: YAML types an unquoted `2026-08-01` as a TIMESTAMP, and
// core.FrontmatterValueFromReader deliberately returns "" for every non-scalar,
// so the shared reader cannot see a date at all. A date is also the only thing
// this chip will show — anything else is left off rather than displayed as
// though the sweep had written it — which keeps the narrow scan honest.
func backlogClosedDate(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	block, _, found := strings.Cut(strings.TrimPrefix(content, "---\n"), "\n---")
	if !found {
		return ""
	}
	if m := backlogClosedRe.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}

// backlogLinksFor builds the spine decorations for the backlog directory dir:
// a delegation chip for every link naming a sub-backlog's spine, a state dot
// for every link naming a ticket beside the spine. exists answers membership
// from the tree the view already loaded; blob reads a sub-spine's bytes so its
// progress can be counted. A missing piece yields no decoration, never an
// error — a chip is an affordance, and the page must render without it.
func backlogLinksFor(dir string, exists func(relPath string) bool, blob func(relPath string) (string, bool)) *backlogLinks {
	if dir == "" || dir == "." || exists == nil || blob == nil {
		return nil
	}
	return &backlogLinks{
		delegate: func(target string) (backlogDelegation, bool) {
			name, ok := backlogLinkSegment(dir, target, true)
			if !ok {
				return backlogDelegation{}, false
			}
			spine := dir + "/" + name + "/" + backlogIndexName
			if !exists(spine) {
				return backlogDelegation{}, false
			}
			content, ok := blob(spine)
			if !ok {
				return backlogDelegation{}, false
			}
			done, total := backlogProgress(scanBacklog(stripFrontmatter(content)))
			if total == 0 {
				return backlogDelegation{}, false
			}
			return backlogDelegation{name: name, done: done, total: total}, true
		},
		ticket: func(target string) bool {
			name, ok := backlogLinkSegment(dir, target, false)
			return ok && exists(dir+"/"+name+".md")
		},
	}
}

// backlogLinkSegment reads a wikilink target as a name inside the backlog
// directory dir: `<sub>/INDEX` when wantIndex, a bare `<ticket>` otherwise.
// Both the fully-qualified form the RFC writes (`[[backlog/voice/INDEX]]`) and
// the short one (`[[voice/INDEX]]`) are accepted; a `#anchor` is dropped, and a
// `.md` suffix tolerated.
//
// This is deliberately conservative. A wikilink resolves by NAME across the
// whole instance, so a target that merely looks like a ticket may point
// somewhere else entirely; only a single segment naming a file that really sits
// in this directory earns a decoration. The name is validated before it becomes
// either a blob path or chip text.
func backlogLinkSegment(dir, target string, wantIndex bool) (string, bool) {
	name := strings.TrimSpace(target)
	if i := strings.IndexByte(name, '#'); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), "/"))
	if ext := path.Ext(name); strings.EqualFold(ext, ".md") {
		name = name[:len(name)-len(ext)]
	}
	if strings.HasPrefix(name, dir+"/") {
		name = name[len(dir)+1:]
	}
	segs := strings.Split(name, "/")
	if wantIndex {
		if len(segs) != 2 || !strings.EqualFold(segs[1], strings.TrimSuffix(backlogIndexName, ".md")) {
			return "", false
		}
	} else if len(segs) != 1 {
		return "", false
	}
	name = segs[0]
	if !backlogNameRe.MatchString(name) ||
		strings.EqualFold(name, backlogArchiveDirName) ||
		strings.EqualFold(name, strings.TrimSuffix(backlogIndexName, ".md")) {
		return "", false
	}
	return name, true
}
