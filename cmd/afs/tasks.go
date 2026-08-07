package main

// `afs tasks` and `afs prime` — the two read-only views of the backlog page and
// the session it starts. No capability lives here: core parses the page, decides
// what is ready, and assembles the pack; this file only parses flags and shapes
// the text a human (or an agent reading a terminal) sees.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

const tasksUsage = `usage: afs tasks [path] [--all|--ready|--band NAME] [--json]`

// noBacklogGuidance is what an instance without a backlog page gets. It is
// guidance on stdout and exit 0, not an error: having no backlog is a normal
// state for an instance nobody has planned work in yet, and the useful answer
// is how to get one, not a non-zero exit for a script to trip over.
const noBacklogGuidance = `No backlog page in this instance.

A backlog is a single page whose own frontmatter declares ` + "`agentsfs_role: backlog`" + ` —
prioritized work in checkbox lists under ## Now / ## Next / ## Later / ## Someday
/ ## Done. The page carries its own conventions, so nothing else has to be
configured. The template lays one down at backlog.md.

Write one yourself, or run ` + "`afs contract upgrade`" + `, which creates the template's
backlog.md when no page already claims the role (it never overwrites one).`

func runTasks(args []string) {
	var all, ready, asJSON bool
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
	if len(pos) > 1 {
		fail(fmt.Errorf("%s", tasksUsage))
	}
	scopes := 0
	for _, on := range []bool{all, ready, band != ""} {
		if on {
			scopes++
		}
	}
	if scopes > 1 {
		fail(fmt.Errorf("--all, --ready, and --band select different scopes; pass one\n%s", tasksUsage))
	}

	backlog, found, err := core.LoadBacklog(instanceRoot(pos, 0))
	if err != nil {
		fail(err)
	}
	if !found {
		if asJSON {
			printTasksJSON(nil, nil)
			return
		}
		fmt.Println(noBacklogGuidance)
		return
	}

	// Scope selection is shared by both output modes, so `--ready --json` and
	// `--ready` answer the same question in two encodings. The default and --all
	// both publish every task in JSON: each record carries status, band, and
	// ready, so a program filters better than a flag could.
	var tasks []*core.Task
	switch {
	case ready:
		tasks = backlog.ReadyTasks()
	case band != "":
		tasks = backlog.TasksInBand(band)
	default:
		tasks = backlog.Flat()
	}
	if asJSON {
		printTasksJSON(&backlog.Path, tasks)
		return
	}

	switch {
	case ready:
		printReadyTasks(tasks)
	case band != "":
		printBandTasks(band, tasks)
	case all:
		printAllTasks(tasks)
	default:
		printDefaultTasks(backlog)
	}
}

func printTasksJSON(backlogPath *string, tasks []*core.Task) {
	if tasks == nil {
		tasks = []*core.Task{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	body := struct {
		// A pointer so an instance with no backlog emits `"backlog": null` —
		// distinguishable from a backlog that exists and happens to be empty.
		Backlog *string      `json:"backlog"`
		Tasks   []*core.Task `json:"tasks"`
	}{Backlog: backlogPath, Tasks: tasks}
	if err := enc.Encode(body); err != nil {
		fail(err)
	}
}

// printDefaultTasks is the working view: what is being worked, what can be
// pulled, and a count of everything else. Blocked, parked, and done tasks are
// counted rather than listed — the default view is for choosing what to do
// next, and `--all` is there for reading the page through the parser.
func printDefaultTasks(b *core.Backlog) {
	printed := false
	if inProgress := b.InProgress(); len(inProgress) > 0 {
		fmt.Println("In progress")
		for _, t := range inProgress {
			fmt.Printf("  %s\n", core.TaskLine(t))
		}
		printed = true
	}
	ready := b.ReadyTasks()
	// ReadyTasks is sorted by band, so a band change is a group boundary; the
	// header is the canonical band name rather than the heading as written, so
	// "## now" and "## Now" don't render as two groups.
	lastBand := ""
	for _, t := range ready {
		if canonical := canonicalBand(t.Band); canonical != lastBand {
			if printed {
				fmt.Println()
			}
			fmt.Println(canonical)
			lastBand, printed = canonical, true
		}
		fmt.Printf("  %s\n", core.TaskLine(t))
	}
	if !printed {
		fmt.Println("Nothing in progress and nothing ready.")
	}
	c := b.Counts()
	fmt.Printf("\n%d blocked · %d parked (Someday) · %d done\n", c.Blocked, c.Parked, c.Done)
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

func printBandTasks(band string, tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Printf("No tasks in band %q.\n", band)
		return
	}
	fmt.Println(bandLabel(tasks[0].Band))
	for _, t := range tasks {
		fmt.Printf("%s%s\n", taskIndent(t.Depth), core.TaskLine(t))
	}
}

// printAllTasks reads the whole page back through the parser: document order,
// grouped by band as the page writes it, nesting preserved. This is the view
// that shows what the parser actually saw — the one to reach for when a task
// isn't showing up where its author expected.
func printAllTasks(tasks []*core.Task) {
	if len(tasks) == 0 {
		fmt.Println("The backlog page has no tasks yet.")
		return
	}
	lastBand, first := "\x00", true
	for _, t := range tasks {
		if t.Band != lastBand {
			if !first {
				fmt.Println()
			}
			fmt.Println(bandLabel(t.Band))
			lastBand, first = t.Band, false
		}
		fmt.Printf("%s%s\n", taskIndent(t.Depth), core.TaskLine(t))
	}
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
