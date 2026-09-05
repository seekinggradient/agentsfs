package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"agentsfs.ai/afs/internal/core"
	"agentsfs.ai/afs/internal/hubclient"
)

// hub connects an agentsfs instance to a hosted agentsfs Hub and uploads it —
// convenience over `git remote add` + `git push`. The shared logic lives in
// internal/hubclient (used by MCP too); this file is the CLI surface.

func runHub(args []string) {
	if len(args) == 0 {
		hubUsage()
		return
	}
	switch args[0] {
	case "login":
		hubLogin(args[1:])
	case "push", "link":
		hubPush(args[1:])
	case "pull", "clone", "get":
		hubPull(args[1:])
	case "list", "repos", "ls":
		hubList()
	case "status":
		hubStatus(args[1:])
	case "credential":
		if len(args) != 2 {
			return // Git only invokes this internal helper with get/store/erase.
		}
		if err := hubclient.HandleCredential(args[1], os.Stdin, os.Stdout); err != nil {
			fail(err)
		}
	case "logout":
		hubclient.Forget()
		fmt.Println("Signed out of the hub on this machine.")
	case "help", "--help", "-h":
		hubUsage()
	default:
		fail(fmt.Errorf("unknown hub command %q; try `afs hub help`", args[0]))
	}
}

func hubUsage() {
	fmt.Print(`afs hub — connect an agentsfs to a hosted Hub and upload it.

  afs hub login [--url URL] [--user NAME] [--token TOKEN]
      Sign in to a hub (default ` + hubclient.DefaultURL + `). Create a token at
      <url>/account. Non-interactive when --user and --token are given.

  afs hub push [owner/name] [--instance PATH]
      Upload committed state. An embedded instance must have an instance-local
      link or an explicit target; its folder name and host-repo remote are never
      guessed. Projection history is appended to the integrated Hub tip.

  afs hub pull [owner/name] --instance PATH [--adopt]
      Fetch Hub commits for a linked embedded instance and merge them under its
      host-repository prefix with the last projection as the three-way base.
      From inside the instance, both the target and --instance may be omitted.
      Resolve conflicts normally, then use --continue; use --abort to undo.

  afs hub pull <name> [dir] [--merge]
      Download a workspace into the current directory. <name> is one of your
      repos (<slug>) or someone else's (<user>/<slug>); dir defaults to ./<slug>.
      Re-run to update an existing checkout. With --merge, fold its files into the
      current instance (or [dir]) instead of nesting them: new files are added,
      identical files skipped, and any file that differs is saved aside under
      scratch/hub-merge-<slug>/ rather than overwriting your copy.

  afs hub list          List your repositories, including workspaces shared with you.
  afs hub status [--instance PATH] [--fetch] [--json]
                        Show sign-in and focused Hub publication state.
  afs hub logout        Forget the saved hub sign-in on this machine.
`)
}

func hubLogin(args []string) {
	url, user, token := "", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--url":
			i++
			url = argAt(args, i)
		case "--user":
			i++
			user = argAt(args, i)
		case "--token":
			i++
			token = argAt(args, i)
		default:
			fail(fmt.Errorf("unknown flag %q", args[i]))
		}
	}
	if url == "" {
		url = hubclient.DefaultURL
	}
	url = strings.TrimRight(url, "/")
	if user == "" {
		user = prompt("Hub username: ")
	}
	if token == "" {
		token = promptSecret("Access token (create one at " + url + "/account): ")
	}
	if user == "" || token == "" {
		fail(errors.New("a username and token are required"))
	}
	if !hubclient.Verify(url, user, token) {
		fail(errors.New("could not sign in — check the username and token"))
	}
	if err := hubclient.Save(hubclient.Config{URL: url, User: user, Token: token}); err != nil {
		fail(err)
	}
	if err := hubclient.EnsureCredentialHelper(); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not install the Git credential helper: %v\n", err)
	}
	fmt.Printf("Signed in to %s as %s.\n", url, user)
}

func hubPush(args []string) {
	name, instancePath := "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--instance":
			i++
			instancePath = argAt(args, i)
		case strings.HasPrefix(a, "-"):
			fail(fmt.Errorf("unknown flag %q", a))
		case name == "":
			name = a
		default:
			fail(errors.New("usage: afs hub push [name] [--instance PATH]"))
		}
	}
	resolution, err := core.ResolveInstance(".", core.ResolveInstanceOptions{ExplicitPath: instancePath, AllowProjectScan: true})
	if err != nil {
		fail(err)
	}
	if resolution.DetectedBy == "project-scan" {
		fmt.Printf("Using embedded AgentsFS: ./%s\n", resolution.Prefix)
	}
	res, err := hubclient.Push(resolution.InstanceRoot, name)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Published committed AgentsFS state to %s/main.\n", res.Slug)
	fmt.Printf("  Instance:    %s\n", res.InstanceRoot)
	fmt.Printf("  Host source: %s on %s\n", shortCLICommit(res.SourceCommit), res.SourceBranch)
	fmt.Printf("  Projection:  %s\n", shortCLICommit(res.ProjectedCommit))
	fmt.Printf("  Verified:    hub/main at %s\n", shortCLICommit(res.VerifiedRemoteCommit))
	fmt.Printf("  Browse:      %s\n", res.ViewURL)
	staged := res.Worktree.StagedCount
	unstaged := res.Worktree.UnstagedCount
	untracked := res.Worktree.UntrackedCount
	conflicted := res.Worktree.ConflictedCount
	if staged+unstaged+untracked+conflicted > 0 {
		fmt.Printf("\nNot included: %d staged, %d unstaged, %d untracked, and %d conflicted path(s) under this instance.\n", staged, unstaged, untracked, conflicted)
		fmt.Println("Commit or resolve them, then run `afs hub push` again.")
	}
}

func hubPull(args []string) {
	var name, dir string
	instancePath := ""
	merge, adopt, continuePull, abortPull := false, false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--merge" || a == "--vendor":
			merge = true
		case a == "--instance":
			i++
			instancePath = argAt(args, i)
		case a == "--adopt":
			adopt = true
		case a == "--continue":
			continuePull = true
		case a == "--abort":
			abortPull = true
		case strings.HasPrefix(a, "-"):
			fail(fmt.Errorf("unknown flag %q", a))
		case name == "":
			name = a
		case dir == "":
			dir = a
		default:
			fail(errors.New("usage: afs hub pull <name> [dir] [--merge]"))
		}
	}
	projectionPull := instancePath != "" || adopt || continuePull || abortPull || name == ""
	if projectionPull {
		if merge || dir != "" {
			fail(errors.New("--merge and a clone directory cannot be combined with embedded projection sync"))
		}
		if continuePull && abortPull {
			fail(errors.New("choose either --continue or --abort"))
		}
		resolution, err := core.ResolveInstance(".", core.ResolveInstanceOptions{ExplicitPath: instancePath, AllowProjectScan: true})
		if err != nil {
			fail(err)
		}
		res, err := hubclient.PullProjection(resolution.InstanceRoot, name, hubclient.ProjectionPullOptions{
			Adopt: adopt, Continue: continuePull, Abort: abortPull,
		})
		if err != nil {
			fail(err)
		}
		if abortPull {
			fmt.Println("Aborted the embedded Hub projection pull; the host worktree was restored.")
			return
		}
		if res.Already {
			fmt.Printf("Already integrated %s at %s; no host commit was needed.\n", res.Repository, shortCLICommit(res.RemoteCommit))
			return
		}
		fmt.Printf("Integrated %s at %s under %s/ in host commit %s.\n", res.Repository, shortCLICommit(res.RemoteCommit), res.Prefix, shortCLICommit(res.HostCommit))
		if res.Adopted {
			fmt.Println("  Adopted byte-identical legacy projection history.")
		}
		fmt.Println("Run `afs hub push` to publish any host-side commits on top of that Hub history.")
		return
	}
	if name == "" {
		fail(errors.New("usage: afs hub pull <name> [dir] [--merge]  (name is <repo> or <user>/<repo>)"))
	}
	res, err := hubclient.Clone(name, dir, merge)
	if err != nil {
		fail(err)
	}
	if res.Merged {
		fmt.Printf("Merged %s/%s into %s\n  %s\n", res.Owner, res.Slug, res.Dir, res.ViewURL)
		fmt.Printf("  %d added, %d identical skipped", len(res.Added), len(res.Skipped))
		if len(res.Conflicts) > 0 {
			fmt.Printf(", %d differed and were NOT overwritten.\n", len(res.Conflicts))
			fmt.Printf("  Remote copies of the differing files were saved under %s/ (your files are untouched):\n", res.QuarantinePath)
			for _, c := range res.Conflicts {
				fmt.Printf("    %s\n", c)
			}
			fmt.Printf("  Reconcile them, then delete %s/.\n", res.QuarantinePath)
		} else {
			fmt.Println(".")
		}
		if len(res.Symlinks) > 0 {
			fmt.Printf("  %d symlink(s) in the remote were not folded (links don't merge): %s\n",
				len(res.Symlinks), strings.Join(res.Symlinks, ", "))
		}
		fmt.Println("  Review the folded files and commit them into this instance to keep them.")
		return
	}
	verb := "Cloned"
	if res.Updated {
		verb = "Updated"
	}
	fmt.Printf("%s %s/%s into %s/\n  %s\n", verb, res.Owner, res.Slug, res.Dir, res.ViewURL)
}

func hubStatus(args []string) {
	instancePath := ""
	fetch, asJSON := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--instance":
			i++
			instancePath = argAt(args, i)
		case "--fetch":
			fetch = true
		case "--json":
			asJSON = true
		default:
			fail(fmt.Errorf("unknown flag %q", args[i]))
		}
	}
	resolution, resolveErr := core.ResolveInstance(".", core.ResolveInstanceOptions{ExplicitPath: instancePath, AllowProjectScan: true})
	root := ""
	if resolveErr == nil {
		root = resolution.InstanceRoot
	}
	s := hubclient.GetStatus(root)
	if asJSON {
		body := struct {
			Account      hubclient.StatusInfo `json:"account"`
			Instance     *core.InstanceStatus `json:"instance,omitempty"`
			ResolveError string               `json:"resolve_error,omitempty"`
		}{Account: s}
		if resolveErr != nil {
			body.ResolveError = resolveErr.Error()
		} else {
			instance := core.InspectInstanceStatus(root, core.StatusOptions{Fetch: fetch})
			body.Instance = &instance
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(body); err != nil {
			fail(err)
		}
		return
	}
	if !s.SignedIn {
		fmt.Println("Not signed in to a hub. Run `afs hub login`.")
		return
	}
	fmt.Printf("Signed in to %s as %s.\n", s.URL, s.User)
	if resolveErr != nil {
		fail(resolveErr)
	}
	st := core.InspectInstanceStatus(root, core.StatusOptions{Fetch: fetch})
	fmt.Printf("Instance:   %s (%s; prefix %s/)\n", st.Path, st.Topology.Mode, st.Topology.Prefix)
	if !st.Publication.Linked {
		fmt.Println("Repository: unlinked")
		fmt.Println("Target:     main")
		fmt.Println("State:      unlinked")
		fmt.Println("Next:       afs hub push")
		return
	}
	fmt.Printf("Repository: %s\n", st.Publication.Repository)
	fmt.Printf("Target:     %s\n", st.Publication.Branch)
	fmt.Printf("Worktree:   %s\n", hubWorktreeSummary(st.Worktree))
	fmt.Printf("Committed:  %s\n", publicationSummary(st.Publication))
	fmt.Printf("Remote:     %s", st.Publication.RemoteState)
	if st.Publication.CachedRemoteCommit != "" {
		fmt.Printf("; hub/main at %s", shortCLICommit(st.Publication.CachedRemoteCommit))
	}
	fmt.Println()
	fmt.Printf("State:      %s\n", st.Publication.State)
	if len(st.NextActions) > 0 {
		fmt.Printf("Next:       %s\n", st.NextActions[len(st.NextActions)-1].Command)
	}
}

func shortCLICommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func hubWorktreeSummary(st core.WorktreeStatus) string {
	if st.Clean {
		return "clean"
	}
	return fmt.Sprintf("%d staged, %d unstaged, %d untracked, %d conflicted", st.StagedCount, st.UnstagedCount, st.UntrackedCount, st.ConflictedCount)
}

func publicationSummary(st core.PublicationStatus) string {
	if st.State == "commits-to-publish" {
		return fmt.Sprintf("%d AgentsFS commit(s) to publish", st.CommitsToPublish)
	}
	return st.State
}

func hubList() {
	repos, err := hubclient.List()
	if err != nil {
		fail(err)
	}
	if len(repos) == 0 {
		fmt.Println("No repositories on the hub yet. Run `afs hub push` from an agentsfs.")
		return
	}
	for _, r := range repos {
		vis := "private"
		if r.Public {
			vis = "public"
		}
		name := r.Name
		access := "owned"
		if r.Shared {
			name = r.Owner + "/" + r.Name
			access = r.Role
		}
		desc := r.Description
		if desc == "" {
			desc = "—"
		}
		fmt.Printf("%-28s  %-7s  %-5s  %3d notes  %-10s  %s\n", name, vis, access, r.Notes, r.Updated, desc)
	}
}

// ---- CLI input helpers ----

func argAt(args []string, i int) string {
	if i >= len(args) {
		fail(errors.New("missing value for a flag"))
	}
	return args[i]
}

func prompt(label string) string {
	fmt.Fprint(os.Stderr, label)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func promptSecret(label string) string {
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
