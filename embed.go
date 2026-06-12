// Package afs holds project-level embedded assets shared by the CLI and,
// later, the MCP server. The canonical template lives in template/ at the
// repo root so humans, docs, and code all point at the same files.
package afs

import "embed"

// TemplateFS is the canonical instance template laid down by `afs init`.
//
//go:embed all:template
var TemplateFS embed.FS
