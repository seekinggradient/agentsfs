// Package afs holds project-level embedded assets shared by the CLI and,
// later, the MCP server. The canonical template lives in template/ at the
// repo root so humans, docs, and code all point at the same files.
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
//go:embed README.md docs/*.md prompts/*.md template/AGENTS.md
var DocsFS embed.FS
