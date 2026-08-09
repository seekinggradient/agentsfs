// Package skills lists and materializes the agent-skill pack bundled in the
// afs binary. Like internal/docs, the set is derived from an embed.FS at
// runtime, so dropping a new skills/<dir>/SKILL.md into the repo ships it with
// no list to keep in sync. afs is harness-neutral: it materializes skills to a
// stable, afs-owned location and tells the caller where to copy them — it never
// writes into ~/.claude/ or any other harness directory.
//
// One skill in the pack, markdownto, is vendored verbatim from another repo
// rather than authored here; skills/markdownto/VERSION pins the bytes and
// internal/skills/markdownto_pin_test.go enforces the pin.
package skills

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	afs "agentsfs.ai/afs"
	"agentsfs.ai/afs/internal/core"
)

// skillsRoot is the embed prefix and skillFile the single file each skill dir
// carries; both match the //go:embed pattern on afs.SkillsFS.
const (
	skillsRoot = "skills"
	skillFile  = "SKILL.md"
)

// Skill is one bundled agent skill: a directory under skills/ holding a
// SKILL.md whose YAML frontmatter names and describes it.
type Skill struct {
	Name        string // frontmatter name:
	Description string // frontmatter description:
	Dir         string // directory basename under skills/
}

// List enumerates the skills embedded in the binary, sorted by directory name.
// The set comes from SkillsFS, never a hardcoded slice, so a new skill ships
// automatically.
func List() ([]Skill, error) {
	entries, err := fs.ReadDir(afs.SkillsFS, skillsRoot)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		data, err := readSkill(dir)
		if err != nil {
			return nil, err
		}
		name := frontmatter(data, "name")
		if name == "" {
			name = dir
		}
		out = append(out, Skill{
			Name:        name,
			Description: frontmatter(data, "description"),
			Dir:         dir,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// Dir is the stable on-disk location afs materializes skills to:
// <XDG_CONFIG_HOME or os.UserConfigDir()>/agentsfs/skills. It resolves the
// config base exactly like core.EmbeddingConfigPath so every afs-owned file
// sits under the same agentsfs config dir.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "agentsfs", "skills"), nil
}

// Materialize writes every bundled skill to baseDir/<dir>/, overwriting
// unconditionally so the on-disk cache always matches this binary (re-run after
// `afs update`). baseDir is a parameter for testability; callers pass Dir().
//
// A skill is its whole directory, not just its SKILL.md: markdownto ships
// byte-normative examples/ beside the instructions, and a copy without them is
// not the skill. Existing files are replaced; files this binary no longer
// carries are left alone rather than deleted, so the cache is never a
// destructive operation on a directory a user may have added to.
func Materialize(baseDir string) error {
	skills, err := List()
	if err != nil {
		return err
	}
	for _, s := range skills {
		root := skillsRoot + "/" + s.Dir
		err := fs.WalkDir(afs.SkillsFS, root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// embed.FS names are always slash-separated; keep the arithmetic in
			// slash space and convert once, so this is correct off Unix too.
			rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
			target := filepath.Join(baseDir, s.Dir, filepath.FromSlash(rel))
			if entry.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := fs.ReadFile(afs.SkillsFS, name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func readSkill(dir string) ([]byte, error) {
	return fs.ReadFile(afs.SkillsFS, skillsRoot+"/"+dir+"/"+skillFile)
}

// frontmatter reuses the CLI's frontmatter parser so `afs skills` reads names
// and descriptions exactly as afs tree/status and the Hub do — one parser, no
// drift.
func frontmatter(data []byte, key string) string {
	return core.FrontmatterValueFromReader(bytes.NewReader(data), key)
}
