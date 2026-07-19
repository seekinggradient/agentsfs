package core

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// The retrieval pipeline. `afs search` is one verb at two output depths:
// ranked pointers (Search) and a hydrated context pack (SearchContext). Both
// share one candidate generator, rankCandidates, which unions several signals
// and merges them with fixed internal weights. There is deliberately no
// --sources/--weights surface: a signal either earns its place against the eval
// set (search_pipeline_test.go) or it is deleted, not flagged off.
//
// The signals:
//   - Body FTS      — porter-stemmed full-text over section bodies, with an
//                     AND→OR fallback so natural-language questions still match.
//   - Description   — frontmatter `description:` lines. chunkInstance already
//                     indexes each as its own FTS row (heading "description"),
//                     so a description match falls straight out of the same
//                     query — curated question-vocabulary, not body content.
//   - Link graph    — 1-hop wikilink neighbours and backlinks of the top seeds,
//                     reusing the existing link resolvers.
//   - Structural    — INDEX.md (the map) and status.md-style files (the "where
//                     do things stand" prior) as always-present low-weight seeds.

// Blend weights. Tuned only against search_pipeline_test.go. Body FTS dominates;
// the reciprocal-rank term (1/(rank+1)) means the top body hit contributes the
// full weight and later hits fall off fast. The structural and link boosts are
// small: they reorder among near-ties and lift a strongly-connected note (a
// status page every note links to) that a natural-language query barely
// lexically matches, without ever outweighing a real content hit.
const (
	wBody   = 1.00 // best body-FTS hit for a file
	wDesc   = 0.70 // best description-FTS hit for a file
	wLink   = 0.30 // pulled in as a 1-hop neighbour/backlink of a top seed
	wStatus = 0.55 // status.md-style structural prior
	wIndex  = 0.15 // INDEX.md structural prior
)

// contractDemotion multiplies the final score of agent-contract files
// (AGENTS.md, CLAUDE.md). They are instructions FOR agents, not knowledge
// content, so for a content question they must never outrank a real note —
// but they stay discoverable when they are the best match (a demotion, not an
// exclusion). Without this, AGENTS.md's broad vocabulary lets it outrank the
// actual answer for vocabulary-poor questions (seen in the eval set).
const contractDemotion = 0.35

func isContractFile(rel string) bool {
	b := baseName(rel)
	return strings.EqualFold(b, "AGENTS.md") || strings.EqualFold(b, "CLAUDE.md")
}

// linkSeedCount is how many top FTS candidates get their link neighbourhood
// expanded. Three keeps expansion focused on the clearly-relevant seeds.
const linkSeedCount = 3

// candidateLimit caps the FTS candidate pool the pipeline reasons over, so
// link expansion and scoring stay cheap regardless of the caller's limit.
const candidateLimit = 60

// candidate is one file under consideration, carrying every signal that fired
// for it. bodyRank/descRank are that file's 0-based position among distinct
// files in the body / description FTS orderings (-1 when the signal did not
// fire), so a reciprocal-rank blend can prefer earlier hits.
type candidate struct {
	path          string
	heading       string // representative section (best body hit, else description)
	snippet       string
	bodyRank      int
	descRank      int
	linkNeighbor  bool
	linkReason    string
	isIndex       bool
	isStatusPrior bool
	score         float64
}

// reason renders the human-readable "why this is here" line for context packs.
func (c *candidate) reason() string {
	var parts []string
	if c.bodyRank >= 0 {
		parts = append(parts, "body fts")
	}
	if c.descRank >= 0 {
		parts = append(parts, "description")
	}
	if c.linkNeighbor && c.linkReason != "" {
		parts = append(parts, c.linkReason)
	}
	if c.isStatusPrior {
		parts = append(parts, "status prior")
	}
	if c.isIndex {
		parts = append(parts, "index seed")
	}
	if len(parts) == 0 {
		return "candidate"
	}
	return "match: " + strings.Join(parts, " + ")
}

// ftsRow is one hit row from the FTS table, before per-file merging.
type ftsRow struct {
	path    string
	heading string
	snippet string
}

// rankCandidates is the whole pipeline up to (not including) hydration: ensure
// the index is fresh, gather FTS rows (AND then OR), fold in description / link
// / structural signals, score, and return the top `limit` files in rank order.
func rankCandidates(root, query string, limit int) ([]candidate, error) {
	db, err := openIndex(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if !ftsFresh(db, root) {
		if _, err := reindexFTS(db, root); err != nil {
			return nil, err
		}
	}

	rows, err := ftsRows(db, query, candidateLimit)
	if err != nil {
		return nil, err
	}

	cands := map[string]*candidate{}
	var seedOrder []string // distinct files in FTS rank order, for link seeding
	get := func(path string) *candidate {
		c := cands[path]
		if c == nil {
			c = &candidate{path: path, bodyRank: -1, descRank: -1}
			cands[path] = c
		}
		return c
	}
	bodyN, descN := 0, 0
	for _, r := range rows {
		c := get(r.path)
		if _, seen := indexOf(seedOrder, r.path); !seen {
			seedOrder = append(seedOrder, r.path)
		}
		if r.heading == "description" {
			if c.descRank < 0 {
				c.descRank = descN
				descN++
			}
			// Only stand in as the representative section when no body hit will.
			if c.bodyRank < 0 && c.heading == "" {
				c.heading, c.snippet = "description", r.snippet
			}
			continue
		}
		if c.bodyRank < 0 {
			c.bodyRank = bodyN
			bodyN++
			c.heading, c.snippet = r.heading, r.snippet
		}
	}

	// Link expansion around the top seeds. Building the graph once and querying
	// it in memory avoids rescanning the instance per seed.
	seeds := seedOrder
	if len(seeds) > linkSeedCount {
		seeds = seeds[:linkSeedCount]
	}
	if len(seeds) > 0 {
		if err := expandLinks(root, seeds, get); err != nil {
			return nil, err
		}
	}

	// Structural seeds are unconditional and low weight. They only surface when
	// little else matched (the whole point for "where do things stand" queries),
	// and never outrank a real content hit.
	if err := addStructuralSeeds(root, get); err != nil {
		return nil, err
	}

	scored := make([]candidate, 0, len(cands))
	for _, c := range cands {
		c.score = scoreCandidate(c)
		if isContractFile(c.path) {
			c.score *= contractDemotion
		}
		if c.heading == "" && c.snippet == "" {
			// A purely structural/link candidate with no indexed body: fall back
			// to its description so the pointer is not blank.
			if d := Description(joinRel(root, c.path)); d != "" {
				c.heading, c.snippet = "description", d
			}
		}
		scored = append(scored, *c)
	}
	// Deterministic order: score desc, then path asc for stable ties.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].path < scored[j].path
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

func scoreCandidate(c *candidate) float64 {
	s := 0.0
	if c.bodyRank >= 0 {
		s += wBody / float64(c.bodyRank+1)
	}
	if c.descRank >= 0 {
		s += wDesc / float64(c.descRank+1)
	}
	if c.linkNeighbor {
		s += wLink
	}
	if c.isStatusPrior {
		s += wStatus
	}
	if c.isIndex {
		s += wIndex
	}
	return s
}

// ftsRows runs the FTS query and returns hit rows in BM25 rank order. It applies
// the AND→OR fallback: the precise all-terms query first, and only if that
// finds nothing (and the query has ≥2 terms) the OR query, so single-term and
// well-matched queries keep their precision.
func ftsRows(db *sql.DB, query string, limit int) ([]ftsRow, error) {
	run := func(match string) ([]ftsRow, error) {
		rows, err := db.Query(
			`SELECT path, heading, snippet(docs_fts, 2, '«', '»', '…', 14)
			 FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT ?`,
			match, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []ftsRow
		for rows.Next() {
			var r ftsRow
			if err := rows.Scan(&r.path, &r.heading, &r.snippet); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	out, err := run(ftsQuery(query))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 && len(strings.Fields(query)) >= 2 {
		return run(ftsQueryOr(query))
	}
	return out, nil
}

// expandLinks pulls each seed's 1-hop wikilink neighbours (files the seed links
// to) and backlinks (files that link to the seed) into the candidate set. The
// link graph is built once and traversed in memory.
func expandLinks(root string, seeds []string, get func(string) *candidate) error {
	links, err := ScanLinks(root)
	if err != nil {
		return err
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		return err
	}
	seedSet := map[string]bool{}
	for _, s := range seeds {
		seedSet[s] = true
	}
	mark := func(path, reason string) {
		if seedSet[path] {
			return // a seed does not link-boost itself
		}
		c := get(path)
		if !c.linkNeighbor {
			c.linkNeighbor, c.linkReason = true, reason
		}
	}
	// Resolve every link once to its target files, then dispatch both directions.
	for _, l := range links {
		targets := idx.ResolveLink(l)
		if seedSet[l.Source] {
			// seed → target: target is linked from the seed.
			for _, t := range targets {
				mark(t, "linked from "+seedName(l.Source))
			}
		}
		for _, t := range targets {
			if seedSet[t] {
				// source → seed: source links to the seed.
				mark(l.Source, "links to "+seedName(t))
				break
			}
		}
	}
	return nil
}

// addStructuralSeeds injects the always-present low-weight candidates: the root
// INDEX.md (the instance map) and every status.md-style file (the "current
// state / next actions" prior the design calls out). Missing files are skipped.
func addStructuralSeeds(root string, get func(string) *candidate) error {
	entries, err := ListEntries(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if e.Rel == "INDEX.md" {
			get(e.Rel).isIndex = true
		}
		if strings.EqualFold(baseName(e.Rel), "status.md") {
			get(e.Rel).isStatusPrior = true
		}
	}
	return nil
}

// seedName is the seed's file name without extension, for readable reasons.
func seedName(rel string) string {
	return strings.TrimSuffix(baseName(rel), ".md")
}

func indexOf(s []string, v string) (int, bool) {
	for i, x := range s {
		if x == v {
			return i, true
		}
	}
	return -1, false
}

// --- Context (hydrated pack) output depth ---------------------------------

// defaultContextBudget is the default --context token estimate ceiling.
const defaultContextBudget = 4000

// ContextDoc is one hydrated document in a context pack.
type ContextDoc struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
	Content     string `json:"content"`
}

// ContextPack is the hydrated form of a search: the top-ranked results with
// their full contents, assembled within a token-estimate budget, plus a
// provenance list of the included paths.
type ContextPack struct {
	Docs                []ContextDoc `json:"docs"`
	BudgetUsedEstTokens int          `json:"budget_used_est_tokens"`
	Pointers            []string     `json:"pointers"`
}

// SearchContext hydrates the top-ranked results into a context pack. Documents
// are added whole in rank order until the estimated-token budget (chars ÷ 4)
// would be exceeded; a document larger than the remaining room contributes only
// its matched section instead, and the top hit is truncated to fit rather than
// dropped so the pack is never empty. budget ≤ 0 uses the default.
func SearchContext(root, query string, budget int) (ContextPack, error) {
	if budget <= 0 {
		budget = defaultContextBudget
	}
	// Rank a generous candidate pool; the budget, not a count, bounds the pack.
	cands, err := rankCandidates(root, query, candidateLimit)
	if err != nil {
		return ContextPack{}, err
	}
	pack := ContextPack{Docs: []ContextDoc{}, Pointers: []string{}}
	used := 0
	for _, c := range cands {
		full, err := os.ReadFile(joinRel(root, c.path))
		if err != nil {
			continue // a candidate whose file we cannot read contributes nothing
		}
		desc := Description(joinRel(root, c.path))
		reason := c.reason()
		headerEst := estTokens(c.path + desc + reason)

		content := string(full)
		est := headerEst + estTokens(content)
		if used+est > budget {
			// The whole file will not fit in the remaining room. Fall back to the
			// matched section; if even that will not fit, stop — unless nothing has
			// been included yet, in which case truncate the top hit to the budget
			// so the pack always answers with something.
			section := matchedSection(content, c.heading)
			est = headerEst + estTokens(section)
			if used+est > budget {
				if len(pack.Docs) > 0 {
					break
				}
				section = truncateToTokens(section, budget-headerEst)
				est = headerEst + estTokens(section)
			}
			content = section
		}
		pack.Docs = append(pack.Docs, ContextDoc{
			Path:        c.path,
			Description: desc,
			Reason:      reason,
			Content:     content,
		})
		pack.Pointers = append(pack.Pointers, c.path)
		used += est
	}
	pack.BudgetUsedEstTokens = used
	return pack, nil
}

// estTokens estimates token count as characters ÷ 4 — the interface unit every
// consumer budgets in, with no model-specific tokenizer dependency. It is
// documented as an estimate precisely because it is one.
func estTokens(s string) int {
	return utf8.RuneCountInString(s) / 4
}

// truncateToTokens trims s to at most n estimated tokens (n×4 runes).
func truncateToTokens(s string, n int) string {
	if n <= 0 {
		return ""
	}
	limit := n * 4
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// matchedSection returns the slice of content under the given heading: the
// "## heading" block for a section hit, or the pre-first-heading intro for a
// file-level / description hit. It falls back to the whole content when the
// heading cannot be located, so an oversized file still contributes its lead.
func matchedSection(content, heading string) string {
	lines := strings.Split(content, "\n")
	if heading == "" || heading == "description" {
		return intro(lines)
	}
	for i, line := range lines {
		if h, ok := strings.CutPrefix(line, "## "); ok && strings.TrimSpace(h) == heading {
			var b strings.Builder
			b.WriteString(line)
			b.WriteString("\n")
			for _, l := range lines[i+1:] {
				if strings.HasPrefix(l, "## ") {
					break
				}
				b.WriteString(l)
				b.WriteString("\n")
			}
			return strings.TrimRight(b.String(), "\n")
		}
	}
	// Heading is likely the file's base name (the intro chunk) — return the lead.
	if lead := intro(lines); lead != "" {
		return lead
	}
	return content
}

// intro is everything before the first "## " section (frontmatter and H1
// included), the natural lead of a file.
func intro(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderContextPack renders a pack as plain text for humans: a per-doc header
// (path, description, why-included), the content, and a provenance footer.
func RenderContextPack(p ContextPack) string {
	var b strings.Builder
	for i, d := range p.Docs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "=== %s ===\n", d.Path)
		if d.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", d.Description)
		}
		fmt.Fprintf(&b, "why: %s\n\n", d.Reason)
		b.WriteString(d.Content)
	}
	b.WriteString("\n\n--- provenance ---\n")
	for _, p := range p.Pointers {
		fmt.Fprintf(&b, "%s\n", p)
	}
	fmt.Fprintf(&b, "budget: ~%d estimated tokens (chars/4)\n", p.BudgetUsedEstTokens)
	return b.String()
}
