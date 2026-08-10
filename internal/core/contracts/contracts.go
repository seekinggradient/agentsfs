// Package contracts vendors the stock AGENTS.md text of each released
// contract version. Upgrade uses these to tell an untouched contract (safe to
// replace) from a hand-adapted one (refuse without --force), and `afs contract
// diff` renders them against the instance's current file. They are internal
// build assets — distinct from template/, which is the live template laid down
// by `afs init` — so they follow the same go:embed pattern but ship in this
// package rather than at the repo root.
package contracts

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed AGENTS-*.md
var fsys embed.FS

// StockContract returns the vendored stock AGENTS.md text for a released
// contract version (e.g. "0.3.0"), and whether one is vendored. Only released
// versions with a byte-exact stock text are present; the current bundled
// version lives in template/ and is served from there, not here.
func StockContract(version string) (string, bool) {
	data, err := fs.ReadFile(fsys, "AGENTS-"+version+".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

//go:embed variants/AGENTS-*.md
var variantContracts embed.FS

// StockContractVariants returns every text a released binary ever shipped as
// the stock AGENTS.md of a contract version: the canonical one first, then any
// vendored variants.
//
// More than one exists because the template kept being edited after a contract
// bump without bumping the version again — 0.9.0 shipped one text in afs
// 0.8.0–0.10.0 and a revised one in 0.11.x, and the 0.2.0 era did it four
// times. Both texts are genuinely pristine, so a customization check that knows
// only the last one calls untouched instances "customized", upgrade refuses,
// and the agent is sent to port adaptations that do not exist. That incident is
// what this list prevents. Only equality checks may use it; anything rendering
// a diff wants the single canonical text from StockContract.
func StockContractVariants(version string) []string {
	var out []string
	if canonical, ok := StockContract(version); ok {
		out = append(out, canonical)
	}
	// Variants are named AGENTS-<version>-<n>.md, oldest first.
	entries, err := fs.ReadDir(variantContracts, "variants")
	if err != nil {
		return out
	}
	prefix := "AGENTS-" + version + "-"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if data, err := fs.ReadFile(variantContracts, "variants/"+e.Name()); err == nil {
			out = append(out, string(data))
		}
	}
	return out
}

//go:embed journal-INDEX-*.md scratch-INDEX-*.md
var reservedIndexes embed.FS

// StockReservedIndex returns the vendored stock INDEX.md text of a reserved
// role's classic (pre-0.4.0) directory for a released contract version, and
// whether one is vendored. The mark-in-place migration compares an instance's
// classic journal/ or scratch/ INDEX against this to decide whether it's stock
// (safe to add the marker in place) or repurposed (leave alone).
func StockReservedIndex(role, version string) (string, bool) {
	data, err := fs.ReadFile(reservedIndexes, role+"-INDEX-"+version+".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

//go:embed backlog-[0-9]*.md
var backlogPages embed.FS

// StockBacklogPage returns the vendored stock backlog PAGE for a released
// contract version, and whether one is vendored. The page shape is contract
// 0.10.0's retired page-level role — 0.11.0 moved the backlog into a directory,
// whose spine is served by StockBacklogSpine — so this only ever answers for the
// versions that shipped a page. The legacy-backlog migration compares an
// instance's page against it to tell an untouched stock page from one the
// instance has made its own.
func StockBacklogPage(version string) (string, bool) {
	data, err := fs.ReadFile(backlogPages, "backlog-"+version+".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

//go:embed backlog-INDEX-*.md
var backlogSpines embed.FS

// StockBacklogSpine returns the vendored stock text of the backlog directory's
// INDEX.md — the spine — for a released contract version, and whether one is
// vendored. It is the backlog's counterpart to StockReservedIndex: since 0.11.0
// the backlog is a directory role, so its stock text is a directory INDEX.
func StockBacklogSpine(version string) (string, bool) {
	data, err := fs.ReadFile(backlogSpines, "backlog-INDEX-"+version+".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}
