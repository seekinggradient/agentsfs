package main

import (
	"bufio"
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
		hubStatus()
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

  afs hub push [name]
      Upload the current agentsfs to the hub as <name> (default: this folder's
      name). Adds a "hub" git remote and pushes. Repeatable to sync updates.

  afs hub pull <name> [dir] [--merge]
      Download a knowledgebase into the current directory. <name> is one of your
      repos (<slug>) or someone else's (<user>/<slug>); dir defaults to ./<slug>.
      Re-run to update an existing checkout. With --merge, drop the pulled repo's
      .git so its notes fold into the current instance (combine knowledgebases).

  afs hub list          List your repositories, including knowledge bases shared with you.
  afs hub status        Show sign-in and whether this agentsfs is linked.
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
	name := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fail(fmt.Errorf("unknown flag %q", a))
		}
		if name == "" {
			name = a
		}
	}
	root, err := core.FindRoot(".")
	if err != nil {
		fail(err)
	}
	res, err := hubclient.Push(root, name)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Uploaded %s to %s\n", res.Slug, res.ViewURL)
}

func hubPull(args []string) {
	var name, dir string
	merge := false
	for _, a := range args {
		switch {
		case a == "--merge" || a == "--vendor":
			merge = true
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
	if name == "" {
		fail(errors.New("usage: afs hub pull <name> [dir] [--merge]  (name is <repo> or <user>/<repo>)"))
	}
	res, err := hubclient.Clone(name, dir, merge)
	if err != nil {
		fail(err)
	}
	verb := "Cloned"
	switch {
	case res.Merged:
		verb = "Merged"
	case res.Updated:
		verb = "Updated"
	}
	fmt.Printf("%s %s/%s into %s/\n  %s\n", verb, res.Owner, res.Slug, res.Dir, res.ViewURL)
	if res.Merged {
		fmt.Println("  (dropped its .git — commit these files into this instance to keep them)")
	}
}

func hubStatus() {
	root, _ := core.FindRoot(".")
	s := hubclient.GetStatus(root)
	if !s.SignedIn {
		fmt.Println("Not signed in to a hub. Run `afs hub login`.")
		return
	}
	fmt.Printf("Signed in to %s as %s.\n", s.URL, s.User)
	if root == "" {
		return
	}
	if s.Linked {
		fmt.Printf("This agentsfs is linked: %s\n", s.LinkedURL)
	} else {
		fmt.Println("This agentsfs is not linked yet — run `afs hub push`.")
	}
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
