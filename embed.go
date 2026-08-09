// Package afs holds project-level embedded assets shared by the CLI and the
// MCP server. The canonical template lives in template/ at the repo root so
// humans, docs, and code all point at the same files.
package afs

import "embed"

// TemplateFS is the canonical instance template laid down by `afs init`.
//
//go:embed all:template
var TemplateFS embed.FS

// DocsFS is the agent-facing and human-facing documentation shipped inside
// the afs binary. Commands like `afs docs agent-start` must work from any
// workspace, even before an agentsfs instance exists.
//
// It reaches outside docs/ twice, both deliberate: template/AGENTS.md is the
// contract as agents read it (`afs docs contract`), and the markdownto SKILL.md
// is a bundled skill that doubles as a docs topic, so an agent working through
// an MCP server — with no local skills directory to load from — can still read
// it (`afs docs markdownto`; internal/docs/docs.go).
//
//go:embed README.md docs/*.md prompts/*.md template/AGENTS.md skills/markdownto/SKILL.md
var DocsFS embed.FS

// SkillsFS is the agent-skill pack shipped inside the afs binary, so `afs
// skills` can list and materialize it from any install without a repo checkout.
// Four AgentsFS skills (agentsfs-setup, -remember, -adopt, -garden) plus the
// vendored markdownto skill (skills/markdownto/VERSION pins it).
//
// The pattern is the whole directory, not skills/*/SKILL.md: a skill may carry
// supporting files beside its SKILL.md — markdownto ships byte-normative
// examples/ — and materializing a skill without them ships a broken copy.
// Files beginning with "." or "_" are excluded, as embed does by default.
//
//go:embed skills
var SkillsFS embed.FS
