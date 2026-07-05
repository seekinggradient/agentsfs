package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ErrStale means the file changed since the editor loaded it (optimistic
// concurrency): the caller should reload and retry.
var ErrStale = errors.New("the file changed since you started editing")

// CommitFile writes content to filePath in a bare repo as a real git commit,
// with no working tree — the write path for in-browser editing. The commit's
// author is the human; the committer is the hub, so `git blame` stays truthful
// about who wrote what. expectedHead (if non-empty) enforces optimistic
// concurrency: the commit only lands if the branch is still at expectedHead.
func CommitFile(gitPath, bareDir, filePath, content, authorName, message, expectedHead string) (newHead string, err error) {
	branchRef, err := gitCmd(gitPath, bareDir, nil, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branchRef = strings.TrimSpace(branchRef)

	head, err := gitCmd(gitPath, bareDir, nil, nil, "rev-parse", branchRef)
	if err != nil {
		return "", err
	}
	head = strings.TrimSpace(head)
	if expectedHead != "" && head != expectedHead {
		return "", ErrStale
	}

	// Write the new blob into the object store.
	blob, err := gitCmd(gitPath, bareDir, nil, strings.NewReader(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	blob = strings.TrimSpace(blob)

	// Build the new tree via a throwaway index seeded from HEAD.
	idx, err := os.CreateTemp("", "afs-idx-*")
	if err != nil {
		return "", err
	}
	idx.Close()
	defer os.Remove(idx.Name())
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx.Name())

	if _, err := gitCmd(gitPath, bareDir, env, nil, "read-tree", head); err != nil {
		return "", err
	}
	if _, err := gitCmd(gitPath, bareDir, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+filePath); err != nil {
		return "", err
	}
	tree, err := gitCmd(gitPath, bareDir, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)

	if strings.TrimSpace(message) == "" {
		message = "Update " + filePath
	}
	commitEnv := append(env,
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorName+"@users.agentsfs",
		"GIT_COMMITTER_NAME=agentsfs hub", "GIT_COMMITTER_EMAIL=hub@agentsfs",
	)
	commit, err := gitCmd(gitPath, bareDir, commitEnv, strings.NewReader(message), "commit-tree", tree, "-p", head, "-F", "-")
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)

	// Compare-and-swap the branch: fails if HEAD moved since we read it.
	if _, err := gitCmd(gitPath, bareDir, nil, nil, "update-ref", branchRef, commit, head); err != nil {
		return "", ErrStale
	}
	return commit, nil
}

// gitCmd runs `git -C bareDir args...` with an optional custom environment and
// stdin, returning stdout. Stderr is folded into the error for diagnostics.
func gitCmd(gitPath, bareDir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command(gitPath, append([]string{"-C", bareDir}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %v: %s", args, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
