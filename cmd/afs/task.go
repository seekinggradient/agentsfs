package main

// `afs task claim` and `afs task done` — the two state flips an agent makes over
// and over, as one command each. They are conveniences over edits any agent can
// make by hand: the substrate is markdown, and everything here writes the same
// bytes a careful editor would, then commits and pushes the way the contract
// asks every unit of work to be committed and pushed.
//
// The write is deliberately the smallest one that can be correct: one character
// on one line of the spine, plus an appended dated line in the ticket's `## Log`
// when the task has earned a ticket file. Nothing else in either file is
// rewritten, so a claim can never reflow, reorder, or reformat someone's page.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/core"
	"agentsfs.ai/afs/internal/hubclient"
)

const taskUsage = `usage: afs task claim <slug> [path] [--dry-run]
       afs task done <slug> [path] [--drop] [--dry-run]`

// taskMarkerRe splits a task line around its checkbox marker so only that one
// byte is replaced. It is the CLI half of core's task grammar: the same list
// bullet and the same four markers, anchored the same way.
var taskMarkerRe = regexp.MustCompile(`^([ \t]*[-*+][ \t]+\[)([ xX/\-])(\])`)

// logHeadingRe matches the ticket's append-only log section heading.
var logHeadingRe = regexp.MustCompile(`(?i)^#{1,6}[ \t]+log[ \t]*$`)

func runTask(args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("%s", taskUsage))
	}
	switch args[0] {
	case "claim":
		runTaskFlip(args[1:], "claim")
	case "done":
		runTaskFlip(args[1:], "done")
	case "help", "--help", "-h":
		fmt.Println(taskUsage)
	default:
		fail(fmt.Errorf("unknown task command %q\n%s", args[0], taskUsage))
	}
}

func runTaskFlip(args []string, verb string) {
	var drop, dryRun bool
	var pos []string
	for _, a := range args {
		switch a {
		case "--drop":
			if verb != "done" {
				fail(fmt.Errorf("--drop only applies to `afs task done`\n%s", taskUsage))
			}
			drop = true
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q\n%s", a, taskUsage))
			}
			pos = append(pos, a)
		}
	}
	if len(pos) < 1 || len(pos) > 2 {
		fail(fmt.Errorf("%s", taskUsage))
	}
	slug := strings.TrimPrefix(pos[0], "^")
	root := instanceRoot(pos, 1)

	backlog, found, err := core.LoadBacklog(root)
	if err != nil {
		fail(err)
	}
	if !found {
		fail(fmt.Errorf("no backlog in %s — nothing to %s (see `afs tasks`)", root, verb))
	}
	task := findTaskBySlug(backlog, slug)

	marker, past := " ", "claimed"
	switch {
	case verb == "claim":
		marker = "/"
		if task.Status != core.TaskOpen {
			fail(fmt.Errorf("%s:%d: ^%s is %s, not open — claim takes an open task ([ ]); resume it or pick another with `afs tasks --ready`",
				task.Page, task.Line, slug, statusLabel(task.Status)))
		}
	case drop:
		marker, past = "-", "dropped"
		requireClosable(task, slug, "drop")
	default:
		marker, past = "x", "done"
		requireClosable(task, slug, "close")
	}

	ticket := ticketFor(root, backlog, task)
	logLine := fmt.Sprintf("- %s — %s", time.Now().UTC().Format("2006-01-02"), past)
	if dryRun {
		fmt.Printf("would set %s:%d to [%s] — %s\n", task.Page, task.Line, marker, core.TaskLine(task))
		if ticket != "" {
			fmt.Printf("would append to %s ## Log: %s\n", ticket, logLine)
		}
		fmt.Printf("would commit: %s: %s\n", verb, slug)
		fmt.Println("--dry-run: nothing was written")
		return
	}

	if err := setTaskMarker(filepath.Join(root, filepath.FromSlash(task.Page)), task.Line, marker); err != nil {
		fail(err)
	}
	changed := []string{task.Page}
	if ticket != "" {
		if err := appendTicketLog(filepath.Join(root, filepath.FromSlash(ticket)), logLine); err != nil {
			fail(err)
		}
		changed = append(changed, ticket)
	}
	fmt.Printf("%s ^%s — %s:%d now [%s]\n", past, slug, task.Page, task.Line, marker)
	if ticket != "" {
		fmt.Printf("  logged in %s\n", ticket)
	}
	commitAndPush(root, changed, fmt.Sprintf("%s: %s", verb, slug), verb)
}

// requireClosable mirrors doctor's task-parent-inconsistent finding, before the
// inconsistency exists rather than after: a parent is complete only when its
// children are, and delegated sub-spine work counts as children across the file
// boundary.
func requireClosable(t *core.Task, slug, action string) {
	if t.Status == core.TaskDone || t.Status == core.TaskDropped {
		fail(fmt.Errorf("%s:%d: ^%s is already %s", t.Page, t.Line, slug, statusLabel(t.Status)))
	}
	if t.OpenChildren > 0 {
		fail(fmt.Errorf("%s:%d: ^%s has %d subtask(s) below it still open or in progress — finish or drop them first, or %s them together by hand",
			t.Page, t.Line, slug, t.OpenChildren, action))
	}
}

func statusLabel(s core.TaskStatus) string {
	switch s {
	case core.TaskInProgress:
		return "in progress ([/])"
	case core.TaskDone:
		return "done ([x])"
	case core.TaskDropped:
		return "dropped ([-])"
	default:
		return "open ([ ])"
	}
}

// findTaskBySlug resolves a slug across every page of the backlog. Slugs are a
// per-page namespace, so the same slug can legitimately appear on the spine and
// on a sub-spine; that is ambiguous here and refused with both locations rather
// than guessed at.
func findTaskBySlug(b *core.Backlog, slug string) *core.Task {
	if slug == "" {
		fail(fmt.Errorf("%s", taskUsage))
	}
	var matches []*core.Task
	pages := map[string]bool{}
	for _, t := range b.Flat() {
		if t.Slug == slug {
			matches = append(matches, t)
			pages[t.Page] = true
		}
	}
	switch {
	case len(matches) == 0:
		fail(fmt.Errorf("no task ^%s in this backlog (slugs are the trailing ^anchor on a task line; `afs tasks --all` lists them)", slug))
	case len(pages) > 1:
		var b strings.Builder
		fmt.Fprintf(&b, "^%s names a task on more than one backlog page; edit the one you mean by hand:", slug)
		for _, t := range matches {
			fmt.Fprintf(&b, "\n  %s:%d  %s", t.Page, t.Line, core.TaskLine(t))
		}
		fail(fmt.Errorf("%s", b.String()))
	}
	// Same page twice is a duplicate slug, which doctor reports; first occurrence
	// wins here exactly as it does everywhere else the backlog resolves one.
	return matches[0]
}

// ticketFor finds the task's detail file: a link on the task line that resolves
// to a file inside the backlog directory which is not itself a task page. A
// ticket is earned, so most tasks have none and this returns "".
func ticketFor(root string, b *core.Backlog, t *core.Task) string {
	if b.Dir == "" || !strings.Contains(t.Text, "[[") {
		return ""
	}
	idx, err := core.BuildNameIndex(root)
	if err != nil {
		return ""
	}
	spines := map[string]bool{}
	for _, p := range b.Pages {
		spines[p] = true
	}
	for _, l := range core.ScanLinksIn(t.Page, t.Text) {
		for _, m := range idx.ResolveLink(l) {
			if spines[m] || !strings.HasPrefix(m, b.Dir+"/") {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m))); err == nil {
				return m
			}
		}
	}
	return ""
}

// setTaskMarker replaces the checkbox marker on one line and nothing else: the
// file is read whole, one byte inside one line is swapped, and the rest —
// including line endings and trailing whitespace — is written back unchanged.
func setTaskMarker(path string, line int, marker string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return fmt.Errorf("%s has no line %d — the backlog changed under this command; re-run it", path, line)
	}
	m := taskMarkerRe.FindStringSubmatchIndex(lines[line-1])
	if m == nil {
		return fmt.Errorf("%s:%d is not a task line any more — the backlog changed under this command; re-run it", path, line)
	}
	lines[line-1] = lines[line-1][:m[4]] + marker + lines[line-1][m[5]:]
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// appendTicketLog appends one dated line to the ticket's `## Log` section,
// creating the section at the end of the file when it has none. The log is
// append-only and newest last, so the line goes at the end of the section.
func appendTicketLog(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if logHeadingRe.MatchString(strings.TrimSuffix(l, "\r")) {
			start = i
		}
	}
	if start < 0 {
		out := strings.TrimRight(text, "\n")
		if out != "" {
			out += "\n"
		}
		out += "\n## Log\n\n" + line + "\n"
		return os.WriteFile(path, []byte(out), 0o644)
	}
	// The section ends at the next heading, or at the end of the file.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			end = i
			break
		}
	}
	body := strings.Split(strings.TrimRight(strings.Join(lines[start+1:end], "\n"), "\n"), "\n")
	if len(body) == 1 && strings.TrimSpace(body[0]) == "" {
		body = nil
	}
	body = append(body, line)
	rebuilt := append([]string{}, lines[:start+1]...)
	rebuilt = append(rebuilt, "")
	rebuilt = append(rebuilt, body...)
	if end < len(lines) {
		rebuilt = append(rebuilt, "")
	}
	rebuilt = append(rebuilt, lines[end:]...)
	out := strings.TrimRight(strings.Join(rebuilt, "\n"), "\n") + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}

// commitAndPush completes the unit of work the way contract rule 12 asks: commit
// what changed, then push it immediately. A non-git instance still gets the
// edit — the markdown is the substrate, git is how it travels — and says so.
func commitAndPush(root string, files []string, message, verb string) {
	if err := gitRun(root, "rev-parse", "--git-dir"); err != nil {
		fmt.Println("  not a git repository — the edit is written but nothing was committed")
		return
	}
	add := append([]string{"add", "--"}, files...)
	if err := gitRun(root, add...); err != nil {
		fail(fmt.Errorf("staging the edit failed: %w", err))
	}
	if err := gitRun(root, "commit", "-m", message); err != nil {
		fail(fmt.Errorf("committing the edit failed (the edit itself is written): %w", err))
	}
	fmt.Printf("  committed: %s\n", message)
	pushAfterFlip(root, verb)
}

// pushAfterFlip pushes best-effort. A REJECTED push is the interesting case: two
// agents claimed the same item, and the loser should take the next ready one
// rather than fight for this line. The local commit is kept either way — it is
// the agent's record of what it tried, and rebasing it is cheaper than
// reconstructing it.
func pushAfterFlip(root, verb string) {
	if hubLinked(root) {
		if _, err := hubclient.Push(root, ""); err != nil {
			if isPushRace(err.Error()) {
				reportRace(verb, err.Error())
			}
			fmt.Fprintf(os.Stderr, "afs task: hub push did not complete: %v\n", err)
			fmt.Fprintln(os.Stderr, "afs task: the commit is local; run `afs hub push` when you can.")
			return
		}
		fmt.Println("  pushed to the hub")
		return
	}
	if !hasRemote(root) {
		fmt.Println("  no remote configured — committed locally only")
		return
	}
	out, err := gitOutput(root, "push")
	if err == nil {
		fmt.Println("  pushed")
		return
	}
	if isPushRace(out) {
		reportRace(verb, out)
	}
	fmt.Fprintf(os.Stderr, "afs task: push did not complete: %s\n", strings.TrimSpace(out))
	fmt.Fprintln(os.Stderr, "afs task: the commit is local; push it when you can.")
}

// reportRace exits non-zero without touching the commit: the flip lost a race,
// which is a normal outcome of a pull-based backlog, not a broken instance.
func reportRace(verb, detail string) {
	fmt.Fprintf(os.Stderr, "afs task: %s raced — the remote moved first, so this push was rejected.\n", verb)
	fmt.Fprintln(os.Stderr, "afs task: your local commit is kept. Pull, then pick the next ready item with `afs tasks --ready`.")
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(os.Stderr, "afs task: git said: %s\n", firstLine(d))
	}
	os.Exit(1)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func isPushRace(detail string) bool {
	d := strings.ToLower(detail)
	for _, marker := range []string{"non-fast-forward", "fetch first", "! [rejected]", "not in this agentsfs projection"} {
		if strings.Contains(d, marker) {
			return true
		}
	}
	return false
}

// hubLinked reports whether this instance publishes to a Hub, by the same two
// signals `afs status` reads: instance-local publication metadata, or a `hub`
// remote on the repository.
func hubLinked(root string) bool {
	if metadata, err := core.LoadPublicationMetadata(root); err == nil && metadata.RemoteURL != "" {
		return true
	}
	out, err := gitOutput(root, "remote", "get-url", "hub")
	return err == nil && strings.TrimSpace(out) != ""
}

func hasRemote(root string) bool {
	out, err := gitOutput(root, "remote")
	return err == nil && strings.TrimSpace(out) != ""
}

func gitRun(root string, args ...string) error {
	_, err := gitOutput(root, args...)
	return err
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
