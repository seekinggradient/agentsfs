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
