package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTaskInstance is a directory-backlog instance in its own git repo, which is
// what claim/done expect: the flip is an edit plus a commit.
func newTaskInstance(t *testing.T, files map[string]string) string {
	t.Helper()
	seed := dirBacklogFiles()
	for rel, content := range files {
		seed[rel] = content
	}
	root := newCLIInstance(t, seed)
	runGit(t, root, "init", "--initial-branch=main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "seed")
	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func gitLogSubject(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--pretty=%s")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// The happy path: one marker flips, the ticket's log gains a dated line, and the
// unit of work is committed.
func TestTaskClaimFlipsTheMarkerLogsAndCommits(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)

	out, err := runAFS(t, root, home, "task", "claim", "prime-design")
	if err != nil {
		t.Fatalf("afs task claim failed: %v\n%s", err, out)
	}
	spine := readFile(t, filepath.Join(root, "backlog", "INDEX.md"))
	if !strings.Contains(spine, "- [/] Prime adaptive tree rendering ^prime-design") {
		t.Errorf("the marker was not flipped:\n%s", spine)
	}
	// Only that one line changed: every other task keeps its own marker and text.
	if !strings.Contains(spine, "- [ ] Update shipped-docs page") ||
		!strings.Contains(spine, "- [/] Embedded hub sync status polish") {
		t.Errorf("claim rewrote lines it should not have:\n%s", spine)
	}
	if subject := gitLogSubject(t, root); subject != "claim: prime-design" {
		t.Errorf("commit subject = %q, want %q", subject, "claim: prime-design")
	}
	if !strings.Contains(out, "no remote configured") {
		t.Errorf("a remoteless instance did not say the commit stayed local:\n%s", out)
	}

	// A task WITH a ticket file gets a dated claim line appended to its log.
	out, err = runAFS(t, root, home, "task", "done", "hub-sync-polish")
	if err == nil {
		t.Fatalf("done should refuse while a subtask is open:\n%s", out)
	}
	out, err = runAFS(t, root, home, "task", "claim", "turn-queue")
	if err != nil {
		t.Fatalf("afs task claim on a sub-spine failed: %v\n%s", err, out)
	}
	sub := readFile(t, filepath.Join(root, "backlog", "voice", "INDEX.md"))
	if !strings.Contains(sub, "- [/] Turn-queue distillation ^turn-queue") {
		t.Errorf("a delegated sub-spine task was not claimed:\n%s", sub)
	}
}

func TestTaskDoneAndDropFlipAndCommit(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)

	if out, err := runAFS(t, root, home, "task", "claim", "prime-design"); err != nil {
		t.Fatalf("setup claim failed: %v\n%s", err, out)
	}
	out, err := runAFS(t, root, home, "task", "done", "prime-design")
	if err != nil {
		t.Fatalf("afs task done failed: %v\n%s", err, out)
	}
	spine := readFile(t, filepath.Join(root, "backlog", "INDEX.md"))
	if !strings.Contains(spine, "- [x] Prime adaptive tree rendering ^prime-design") {
		t.Errorf("done did not flip the marker:\n%s", spine)
	}
	if subject := gitLogSubject(t, root); subject != "done: prime-design" {
		t.Errorf("commit subject = %q", subject)
	}

	// --drop is the abandoned terminal state, same rules.
	if out, err := runAFS(t, root, home, "task", "done", "tts-vendor", "--drop"); err != nil {
		t.Fatalf("afs task done --drop failed: %v\n%s", err, out)
	}
	spine = readFile(t, filepath.Join(root, "backlog", "INDEX.md"))
	if !strings.Contains(spine, "- [-] Pick a TTS vendor") {
		t.Errorf("--drop did not write the dropped marker:\n%s", spine)
	}
	if subject := gitLogSubject(t, root); subject != "done: tts-vendor" {
		t.Errorf("commit subject = %q", subject)
	}
	// Closing what is already closed is refused rather than re-committed.
	if out, err := runAFS(t, root, home, "task", "done", "prime-design"); err == nil {
		t.Fatalf("closing a done task should fail:\n%s", out)
	} else if !strings.Contains(out, "already done") {
		t.Errorf("refusal wording:\n%s", out)
	}
}

// A ticket file's `## Log` is append-only and gains one dated line per flip.
func TestTaskFlipLogsOnTheTicketFile(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, map[string]string{
		"backlog/INDEX.md": `---
description: Project backlog.
agentsfs_role: backlog
---

## Now
- [ ] Hub sync polish → [[backlog/hub-sync]] ^hub-sync
`,
	})

	out, err := runAFS(t, root, home, "task", "claim", "hub-sync")
	if err != nil {
		t.Fatalf("afs task claim failed: %v\n%s", err, out)
	}
	ticket := readFile(t, filepath.Join(root, "backlog", "hub-sync.md"))
	if !strings.Contains(ticket, "- 2026-08-01 — opened") {
		t.Errorf("the existing log was not preserved:\n%s", ticket)
	}
	if !strings.HasSuffix(strings.TrimSpace(ticket), "— claimed") {
		t.Errorf("the claim line was not appended last:\n%s", ticket)
	}
	if !strings.Contains(out, "logged in backlog/hub-sync.md") {
		t.Errorf("the ticket log was not reported:\n%s", out)
	}

	// A ticket with no ## Log section gets one at the end of the file.
	mustWriteFile(t, filepath.Join(root, "backlog", "hub-sync.md"),
		"---\ndescription: The ticket.\n---\n\n# Hub sync\n\nBody.\n")
	if out, err := runAFS(t, root, home, "task", "done", "hub-sync"); err != nil {
		t.Fatalf("afs task done failed: %v\n%s", err, out)
	}
	ticket = readFile(t, filepath.Join(root, "backlog", "hub-sync.md"))
	if !strings.Contains(ticket, "## Log") || !strings.Contains(ticket, "— done") {
		t.Errorf("a log section was not created:\n%s", ticket)
	}
	if !strings.Contains(ticket, "Body.") {
		t.Errorf("appending the log rewrote the body:\n%s", ticket)
	}
}

func TestTaskClaimRefusesANonOpenTask(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)

	out, err := runAFS(t, root, home, "task", "claim", "hub-sync-polish")
	if err == nil {
		t.Fatalf("claiming an in-progress task should fail:\n%s", out)
	}
	for _, want := range []string{"in progress ([/])", "claim takes an open task"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal missing %q:\n%s", want, out)
		}
	}
	if before := readFile(t, filepath.Join(root, "backlog", "INDEX.md")); !strings.Contains(before, "- [/] Embedded hub sync status polish") {
		t.Errorf("a refused claim still edited the spine:\n%s", before)
	}
}

// done mirrors doctor's task-parent-inconsistent finding rather than creating
// the inconsistency it would report.
func TestTaskDoneRefusesOpenChildren(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)

	out, err := runAFS(t, root, home, "task", "done", "hub-sync-polish")
	if err == nil {
		t.Fatalf("closing a parent with open subtasks should fail:\n%s", out)
	}
	if !strings.Contains(out, "still open or in progress") {
		t.Errorf("refusal did not mirror doctor's language:\n%s", out)
	}

	// The cross-file case: a delegating line whose sub-spine still has work.
	out, err = runAFS(t, root, home, "task", "done", "voice-v3")
	if err == nil {
		t.Fatalf("closing a delegation with open sub-spine work should fail:\n%s", out)
	}
	if !strings.Contains(out, "subtask(s)") {
		t.Errorf("delegated children were not counted:\n%s", out)
	}
}

// A slug is a per-page namespace, so the same one can legitimately exist on two
// pages. That is ambiguous to a command that edits one line, and it refuses.
func TestTaskRefusesASlugOnTwoPages(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, map[string]string{
		"backlog/voice/INDEX.md": `---
description: Voice v3 workstream.
---

## Now
- [ ] Voice-side prime work ^prime-design
`,
	})

	out, err := runAFS(t, root, home, "task", "claim", "prime-design")
	if err == nil {
		t.Fatalf("an ambiguous slug should be refused:\n%s", out)
	}
	for _, want := range []string{"more than one backlog page", "backlog/INDEX.md:", "backlog/voice/INDEX.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("ambiguity report missing %q:\n%s", want, out)
		}
	}
}

func TestTaskDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)
	before := readFile(t, filepath.Join(root, "backlog", "INDEX.md"))
	ticketBefore := readFile(t, filepath.Join(root, "backlog", "hub-sync.md"))
	headBefore := gitLogSubject(t, root)

	out, err := runAFS(t, root, home, "task", "claim", "prime-design", "--dry-run")
	if err != nil {
		t.Fatalf("afs task claim --dry-run failed: %v\n%s", err, out)
	}
	for _, want := range []string{"would set backlog/INDEX.md:", "would commit: claim: prime-design", "nothing was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run output missing %q:\n%s", want, out)
		}
	}
	if readFile(t, filepath.Join(root, "backlog", "INDEX.md")) != before {
		t.Errorf("--dry-run edited the spine")
	}
	if readFile(t, filepath.Join(root, "backlog", "hub-sync.md")) != ticketBefore {
		t.Errorf("--dry-run edited the ticket")
	}
	if gitLogSubject(t, root) != headBefore {
		t.Errorf("--dry-run committed something")
	}
}

// Outside git the markdown is still the substrate: the edit lands and the
// command says nothing was committed.
func TestTaskFlipWithoutGitEditsAndSaysSo(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "task", "claim", "prime-design")
	if err != nil {
		t.Fatalf("afs task claim outside git failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not a git repository") {
		t.Errorf("a non-git instance did not say the flip was uncommitted:\n%s", out)
	}
	if spine := readFile(t, filepath.Join(root, "backlog", "INDEX.md")); !strings.Contains(spine, "- [/] Prime adaptive tree rendering") {
		t.Errorf("the edit did not land outside git:\n%s", spine)
	}
}

// A push that loses the race is a normal outcome of a pull-based backlog: the
// local commit is kept and the agent is told to take the next ready item.
func TestTaskClaimReportsALostPushRace(t *testing.T) {
	home := t.TempDir()
	root := newTaskInstance(t, nil)

	bare := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, filepath.Dir(bare), "init", "--bare", "--initial-branch=main", bare)
	runGit(t, root, "remote", "add", "origin", bare)
	runGit(t, root, "push", "-u", "origin", "main")

	// Another checkout pushes first.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", bare, other)
	runGit(t, other, "config", "user.email", "other@example.com")
	runGit(t, other, "config", "user.name", "other")
	mustWriteFile(t, filepath.Join(other, "notes.md"), "---\ndescription: Elsewhere.\n---\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "elsewhere")
	runGit(t, other, "push")

	out, err := runAFS(t, root, home, "task", "claim", "prime-design")
	if err == nil {
		t.Fatalf("a rejected push should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "raced") || !strings.Contains(out, "afs tasks --ready") {
		t.Errorf("the lost race was not reported as one:\n%s", out)
	}
	// The local commit survives — it is the agent's record of what it tried.
	if subject := gitLogSubject(t, root); subject != "claim: prime-design" {
		t.Errorf("the local commit was rolled back: HEAD is %q", subject)
	}
	if spine := readFile(t, filepath.Join(root, "backlog", "INDEX.md")); !strings.Contains(spine, "- [/] Prime adaptive tree rendering") {
		t.Errorf("the edit was rolled back:\n%s", spine)
	}
}
