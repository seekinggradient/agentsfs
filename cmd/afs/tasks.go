package main

// `afs tasks` and `afs prime` — the two read-only views of the backlog and the
// session it starts. No capability lives here: core parses the spine and every
// sub-spine, decides what is ready, reads the archive, and scans the fleet; this
// file only parses flags and shapes the text a human (or an agent reading a
// terminal) sees.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

const tasksUsage = `usage: afs tasks [path|search-root] [--all|--ready|--band NAME|--blocked-on-owner|--done] [--all-instances] [--json]`

// noBacklogGuidance is what an instance without a backlog gets. It is guidance
// on stdout and exit 0, not an error: having no backlog is a normal state for an
// instance nobody has planned work in yet, and the useful answer is how to get
// one, not a non-zero exit for a script to trip over.
const noBacklogGuidance = `No backlog in this instance.

The backlog is a directory whose own ` + "`INDEX.md`" + ` declares
` + "`agentsfs_role: backlog`" + ` in its frontmatter. That INDEX.md is the spine:
prioritized work in checkbox lists under ## Now / ## Next / ## Later / ## Someday
/ ## Done. Ticket detail files sit beside it (earned, never default), archive/
holds what closed, and a subdirectory's INDEX.md is a sub-backlog reached by
delegation from the spine. The template lays one down at backlog/INDEX.md.

A page carrying the marker in its own frontmatter is the retired 0.10.0 shape and
still reads, read-only.

Write one yourself, or run ` + "`afs contract upgrade`" + `, which creates the template's
backlog/INDEX.md when nothing already claims the role (it never overwrites one).`

func runTasks(args []string) {
	var all, ready, asJSON, ownerBlocked, done, allInstances bool
	band := ""
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			all = true
		case arg == "--ready":
			ready = true
		case arg == "--json":
			asJSON = true
		case arg == "--blocked-on-owner":
			ownerBlocked = true
		case arg == "--done":
			done = true
		// --all already means "every task on this backlog", so the fleet switch
		// cannot reuse `afs status`'s spelling. The behavior is the same one:
		// scan the search roots instead of resolving a single instance.
		case arg == "--all-instances":
			allInstances = true
		case arg == "--band":
			if i+1 >= len(args) {
				fail(fmt.Errorf("--band needs a band name (Now, Next, Later, Someday, Done, or a heading this page invented)"))
			}
			i++
			band = args[i]
		case strings.HasPrefix(arg, "--band="):
			band = strings.TrimPrefix(arg, "--band=")
		default:
			if strings.HasPrefix(arg, "-") {
				fail(fmt.Errorf("unknown flag %q\n%s", arg, tasksUsage))
			}
			pos = append(pos, arg)
		}
	}
	if len(pos) > 1 && !allInstances {
		fail(fmt.Errorf("%s", tasksUsage))
	}
	scopes := 0
	for _, on := range []bool{all, ready, band != "", ownerBlocked, done} {
		if on {
			scopes++
		}
	}
	if scopes > 1 {
		fail(fmt.Errorf("--all, --ready, --band, --blocked-on-owner, and --done select different scopes; pass one\n%s", tasksUsage))
	}

	// Mode detection is `afs status`'s: one instance resolving from the
	// positional means the focused view, anything else means the fleet. A
	// positional that is not inside an instance is a search root, not an error.
	root, fleet := "", allInstances
	if !fleet {
		start := "."
		if len(pos) == 1 {
			start = pos[0]
		}
		if resolved, err := core.FindRoot(start); err == nil {
			root = resolved
			noteStaleContract(root)
		} else {
			// Not inside an instance: the argument (or the cwd) is a search root,
			// exactly as `afs status` reads one.
			fleet = true
		}
	}
	if fleet {
		// The fleet view is a triage summary, not a second query language: it
		// answers "which project should I pick up", and --blocked-on-owner narrows
		// it to the owner's questions. The scoped listings are what `afs tasks`
		// inside the chosen instance is for, one command away.
		if all || ready || band != "" || done {
			fail(fmt.Errorf("--all, --ready, --band, and --done read one instance's backlog; run them inside the instance\n%s", tasksUsage))
		}
		runTasksFleet(pos, allInstances, asJSON, ownerBlocked)
		return
	}

	if done {
		runTasksDone(root, asJSON)
		return
	}

	backlog, found, err := core.LoadBacklog(root)
	if err != nil {
		fail(err)
	}
	if !found {
		if asJSON {
			printTasksJSON(nil, nil, nil)
			return
		}
		fmt.Println(noBacklogGuidance)
		return
	}

	// Scope selection is shared by both output modes, so `--ready --json` and
	// `--ready` answer the same question in two encodings. The default and --all
	// both publish every task in JSON: each record carries status, band, ready,
	// and the owner-blocked channel, so a program filters better than a flag could.
	var tasks []*core.Task
	switch {
	case ready:
		tasks = backlog.ReadyTasks()
	case band != "":
		tasks = backlog.TasksInBand(band)
	case ownerBlocked:
		tasks = backlog.OwnerBlocked()
	default:
		tasks = backlog.Flat()
	}
	if asJSON {
		printTasksJSON(&backlog.Spine, backlog.Pages, tasks)
		return
	}

	switch {
	case ready:
		printReadyTasks(tasks)
	case band != "":
		printBandTasks(backlog, band, tasks)
	case ownerBlocked:
		printOwnerInbox(tasks)
	case all:
		printAllTasks(backlog, tasks)
	default:
		printDefaultTasks(backlog)
	}
}

// runTasksDone prints the archive: what closed, when, newest first. It is the
// derived history query — the archive is a collection of markdown, and this
// reads it rather than keeping a second record of anything.
func runTasksDone(root string, asJSON bool) {
	archive, err := core.LoadBacklogArchive(root)
	if err != nil {
		fail(err)
	}
	roles, err := core.ResolveReservedDirs(root)
	if err != nil {
		fail(err)
	}
	if asJSON {
		if archive == nil {
			archive = []core.ArchivedTask{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		var spine *string
		if roles.BacklogSpine != "" {
			s := roles.BacklogSpine
			spine = &s
		}
		body := struct {
			Backlog *string             `json:"backlog"`
			Archive []core.ArchivedTask `json:"archive"`
		}{Backlog: spine, Archive: archive}
		if err := enc.Encode(body); err != nil {
			fail(err)
		}
		return
	}
	if len(archive) == 0 {
		fmt.Println("Nothing archived yet. The gardener sweeps closed work into the backlog's archive/ collection; `afs tasks --all` shows what the spine still carries.")
		return
	}
	// Newest first, and stable within a date so a rollup page's own order (the
	// order the sweep appended) survives.
	sorted := append([]core.ArchivedTask{}, archive...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date > sorted[j].Date })
	lastYear := ""
	for _, a := range sorted {
		if y := archiveYear(a.Date); y != lastYear {
			if lastYear != "" {
				fmt.Println()
			}
			fmt.Println(y)
			lastYear = y
		}
		line := fmt.Sprintf("  %-10s [%s] %s", dateOrDash(a.Date), archiveMarker(a.Status), a.Text)
		if a.Slug != "" {
			line += " [^" + a.Slug + "]"
		}
		fmt.Println(line)
		if a.Ticket != "" {
			fmt.Printf("             %s\n", a.Ticket)
		}
	}
	fmt.Printf("\n%d closed item(s) in %s\n", len(sorted), archiveLabel(roles))
}

func archiveLabel(roles core.RoleDirs) string {
	if roles.Backlog == "" || roles.BacklogLegacy {
		return "the archive"
	}
	return roles.Backlog + "/archive"
}

func archiveYear(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return "(no closed date)"
}

func dateOrDash(date string) string {
	if date == "" {
		return "—"
	}
	return date
}

func archiveMarker(status core.TaskStatus) string {
	if status == core.TaskDropped {
		return "-"
	}
	return "x"
}

// runTasksFleet prints the cross-instance triage view: one group per instance,
// and no ordering invented between them — the owner is the cross-project ranking
// function, and this view only informs which project to pick up.
func runTasksFleet(roots []string, allInstances, asJSON, ownerBlocked bool) {
	if len(roots) == 0 {
		roots = []string{"."}
	}
	report, err := core.TasksAcrossInstances(roots, core.StatusOptions{All: allInstances})
	if err != nil {
		fail(err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fail(err)
		}
		return
	}
	printTasksScopes(report.Scopes, report.SearchRoots)
	if len(report.Instances) == 0 {
		fmt.Printf("No AgentsFS instances found beneath %s.\n", strings.Join(report.SearchRoots, ", "))
	}
	for i, inst := range report.Instances {
		if i > 0 {
			fmt.Println()
		}
		printInstanceTasks(inst, ownerBlocked)
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(os.Stderr, "afs tasks: could not inspect %s: %s\n", issue.Path, issue.Message)
	}
}

func printTasksScopes(scopes []core.StatusScope, searchRoots []string) {
	fmt.Println("Tasks scope: AgentsFS instances discoverable within:")
	if len(scopes) == 0 {
		fmt.Println("  (no valid search roots)")
	}
	for _, scope := range scopes {
		fmt.Printf("  %s\n", scope.SearchRoot)
	}
	for _, scope := range scopes {
		if scope.Complete {
			continue
		}
		fmt.Printf("WARNING: scan incomplete for %s after %d entries (%s); results are partial.\n",
			scope.SearchRoot, scope.EntriesVisited, scope.IncompleteReason)
	}
	fmt.Println()
}

func printInstanceTasks(inst core.InstanceTasks, ownerBlockedOnly bool) {
	fmt.Println(inst.Path)
	if inst.Description != "" {
		fmt.Printf("  %s\n", inst.Description)
	}
	if inst.Error != "" {
		fmt.Printf("  backlog unreadable: %s\n", inst.Error)
		return
	}
	if inst.Backlog == "" {
		fmt.Println("  (no backlog)")
		return
	}
	if !ownerBlockedOnly && len(inst.InProgress) > 0 {
		fmt.Println("  In progress")
		printTaskLines(inst.Backlog, inst.InProgress, "    ")
	}
	if len(inst.OwnerBlocked) > 0 {
		fmt.Println("  Blocked on owner")
		printTaskLines(inst.Backlog, inst.OwnerBlocked, "    ")
	}
	if !ownerBlockedOnly && len(inst.Ready) > 0 {
		fmt.Println("  Ready")
		for _, t := range inst.Ready {
			fmt.Printf("    %-5s %s%s\n", canonicalBand(t.Band), core.TaskLine(t), pageSuffix(inst.Backlog, t))
		}
		if inst.ReadyTotal > len(inst.Ready) {
			fmt.Printf("    … %d more ready\n", inst.ReadyTotal-len(inst.Ready))
		}
	}
	fmt.Printf("  %d ready · %d blocked · %d parked (Someday) · %d done\n",
		inst.ReadyTotal, inst.Counts.Blocked, inst.Counts.Parked, inst.Counts.Done)
}

func printTasksJSON(spine *string, pages []string, tasks []*core.Task) {
	if tasks == nil {
		tasks = []*core.Task{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	body := struct {
		// A pointer so an instance with no backlog emits `"backlog": null` —
		// distinguishable from a backlog that exists and happens to be empty. Since
		// 0.11.0 it is the SPINE's path (the task page), which for a directory
		// backlog is <dir>/INDEX.md.
		Backlog *string `json:"backlog"`
		// Pages lists every task page parsed when there is more than one, so a
		// consumer can tell a delegated sub-spine's tasks from the spine's without
		// re-deriving the set. Absent for the single-page case, which is most.
		Pages []string     `json:"pages,omitempty"`
		Tasks []*core.Task `json:"tasks"`
	}{Backlog: spine, Tasks: tasks}
	if len(pages) > 1 {
		body.Pages = pages
	}
	if err := enc.Encode(body); err != nil {
		fail(err)
	}
}

// printDefaultTasks is the working view: what is being worked, what is waiting
// on the owner, what can be pulled, and a count of everything else. Blocked,
// parked, and done tasks are counted rather than listed — the default view is
// for choosing what to do next, and `--all` is there for reading the backlog
// through the parser.
func printDefaultTasks(b *core.Backlog) {
	printed := false
	if inProgress := b.InProgress(); len(inProgress) > 0 {
		fmt.Println("In progress")
		printTaskLines(b.Spine, inProgress, "  ")
		printed = true
	}
	// The owner-blocked channel sits between what is being worked and what can be
	// pulled: these are questions, not work, and only the reader can clear them.
	if owner := b.OwnerBlocked(); len(owner) > 0 {
		if printed {
			fmt.Println()
		}
		fmt.Println("Blocked on owner")
		printTaskLines(b.Spine, owner, "  ")
		printed = true
	}
	ready := b.ReadyTasks()
	// ReadyTasks is sorted by band, so a band change is a group boundary; the
	// header is the canonical band name rather than the heading as written, so
	// "## now" and "## Now" don't render as two groups.
	lastBand, page := "", b.Spine
	for _, t := range ready {
		if canonical := canonicalBand(t.Band); canonical != lastBand {
			if printed {
				fmt.Println()
			}
			fmt.Println(canonical)
			lastBand, printed, page = canonical, true, b.Spine
		}
		page = printTaskLine(b.Spine, page, t, "  ")
	}
	if !printed {
		fmt.Println("Nothing in progress and nothing ready.")
	}
	c := b.Counts()
	fmt.Printf("\n%d blocked · %d parked (Someday) · %d done\n", c.Blocked, c.Parked, c.Done)
}

// printOwnerInbox is the owner's question queue: every parked question with the
// file and line to edit it away at, which is how the owner answers one.
func printOwnerInbox(tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Println("Nothing is blocked on you.")
		return
	}
	for _, t := range tasks {
		fmt.Printf("%s:%d  %s\n", t.Page, t.Line, core.TaskLine(t))
		if t.OwnerQuestion != "" {
			fmt.Printf("    ? %s\n", t.OwnerQuestion)
		}
	}
	fmt.Printf("\n%d question(s) waiting on you. Answer one by editing the `— blocked by owner:` clause away.\n", len(tasks))
}

func printReadyTasks(tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Println("No ready tasks. `afs tasks --all` shows what is blocked, parked, or done.")
		return
	}
	for _, t := range tasks {
		fmt.Printf("%-5s %s\n", canonicalBand(t.Band), core.TaskLine(t))
	}
}

func printBandTasks(b *core.Backlog, band string, tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Printf("No tasks in band %q.\n", band)
		return
	}
	fmt.Println(bandLabel(tasks[0].Band))
	page := b.Spine
	for _, t := range tasks {
		page = printNestedTaskLine(b.Spine, page, t)
	}
}

// printAllTasks reads the whole backlog back through the parser: document order,
// grouped by band as the spine writes it, nesting preserved, each sub-spine's
// tasks labeled with the page they came from. This is the view that shows what
// the parser actually saw — the one to reach for when a task isn't showing up
// where its author expected.
func printAllTasks(b *core.Backlog, tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Println("The backlog has no tasks yet.")
		return
	}
	lastBand, first, page := "\x00", true, b.Spine
	for _, t := range tasks {
		if t.Band != lastBand {
			if !first {
				fmt.Println()
			}
			fmt.Println(bandLabel(t.Band))
			lastBand, first, page = t.Band, false, b.Spine
		}
		page = printNestedTaskLine(b.Spine, page, t)
	}
}

// printTaskLines prints a group of task lines, labeling the ones that come from
// a delegated sub-spine with their page. A sub-spine's tasks are the delegating
// line's children across a file boundary, so the reader has to be able to see
// which file a line lives on before editing it.
func printTaskLines(spine string, tasks []*core.Task, indent string) {
	page := spine
	for _, t := range tasks {
		page = printTaskLine(spine, page, t, indent)
	}
}

// printTaskLine prints one line, emitting a page header first when the page
// changed away from the spine. It returns the page it printed, which the caller
// carries into the next line.
func printTaskLine(spine, page string, t *core.Task, indent string) string {
	if t.Page != page {
		page = t.Page
		if page != spine {
			fmt.Printf("%s%s:\n", indent, page)
		}
	}
	if page != spine {
		indent += "  "
	}
	fmt.Printf("%s%s\n", indent, core.TaskLine(t))
	return page
}

// printNestedTaskLine is printTaskLine for the views that mirror the page's own
// nesting (--all, --band): the depth indent stays the task's, and a sub-spine
// gets a page header at the depth its delegating line sits.
func printNestedTaskLine(spine, page string, t *core.Task) string {
	if t.Page != page {
		page = t.Page
		if page != spine {
			fmt.Printf("%s%s:\n", taskIndent(t.Depth), page)
		}
	}
	fmt.Printf("%s%s\n", taskIndent(t.Depth), core.TaskLine(t))
	return page
}

// pageSuffix names a task's page when it is not the spine. The fleet view is one
// line per task with no room for a header, so the label rides the line.
func pageSuffix(spine string, t *core.Task) string {
	if t.Page == "" || t.Page == spine {
		return ""
	}
	return "  (" + t.Page + ")"
}

// taskIndent mirrors the page's own nesting so decomposition stays visible: a
// child task is a breakdown of its parent, not a separate item.
func taskIndent(depth int) string {
	return strings.Repeat("  ", depth+1)
}

func bandLabel(band string) string {
	if strings.TrimSpace(band) == "" {
		return "(before any band heading)"
	}
	return band
}

// canonicalBand maps a working band to its canonical spelling for grouping;
// anything else (Someday, Done, an invented heading, no heading) keeps the
// author's own text, since those never appear in a ready listing anyway.
func canonicalBand(band string) string {
	switch strings.ToLower(strings.TrimSpace(band)) {
	case strings.ToLower(core.BandNow):
		return core.BandNow
	case strings.ToLower(core.BandNext):
		return core.BandNext
	case strings.ToLower(core.BandLater):
		return core.BandLater
	default:
		return bandLabel(band)
	}
}
