# Source-following Hub narration

Every Markdown file offers **Guided narration**, and a workspace offers the same
entry point with a page picker. The default reads the page in document order and
highlights its current passage. It does not ask an agent to rewrite, summarize or
explain the page. This is separate from authored `guided-narration@0.1` manuscripts,
which continue to use the existing Markdown To player.

The reader preserves Hub Markdown rendering (headings, lists, tables, math, code,
images and links). Headings form section navigation. Image alt text is spoken;
frontmatter and raw HTML are not. Long blocks are split for speech while retaining
one highlight target. Table cells are read in row order, code literally, and math
uses its source notation; this is not a generated explanation of diagrams or formulas.

Users choose a Gemini voice, playback speed, a section or passage, and whether the
page should scroll with the voice. Selected pages play in listed path order; Select
all includes all Markdown files. The browser remembers the queue, voice, speed and
current passage under a viewer/workspace-specific key. A changed source hash resets
saved position. Empty pages are skipped during continuous playback. Audio and page
contents are not written to browser persistent storage, and listening never writes
the repository.

## Routes and trust boundaries

`/<owner>/<repo>/listen` is a standalone first-party reader. Its `pages`, `source`,
`voices`, and `speech` subroutes inherit the same repository read ACL as file views.
The first two are read-only GETs. `voices` additionally needs an authenticated
browser session; `speech` requires both a session and the same-origin POST check.
A read-only collaborator can listen without receiving write permission. An anonymous
reader of a public repo can inspect the page and must sign in for Gemini speech.

Speech accepts only `{path, hash, index, voice}`. It resolves the source at a pinned
Git commit, derives speech from the safe Markdown AST, and checks the hash before
calling the service. Arbitrary client text and unknown JSON fields are rejected.
Untrusted HTML cannot create highlighting targets or scripts. Markdown is limited
to 1 MiB per page. Existing file/asset ACLs continue to apply to image requests.

Hub forwards to the fixed Markdown To service at
`https://mdto-narrate-api.fly.dev`, using its existing `/v1/narrate/voices` and
`/v1/narrate/synthesize` contracts. A dedicated server-held per-user PAT is stored
in `.narration-pats.json` beside Hub's repositories, with the AgentPATStore's existing
file protections. Revoked credentials are checked and replaced. The browser receives
neither that PAT nor a Gemini key, and cookies/identity headers are not forwarded.
Redirects are refused. The configured service owns the Gemini model and rate limits.

The Hub permits two pending syntheses per user, deduplicates identical in-flight
requests, and keeps a bounded 32 MiB, 30-minute response cache scoped by user,
repository, source hash, voice, service and spoken text. Failed or uncertain requests
cool down for one minute instead of immediately resubmitting paid synthesis. Successful
responses must contain valid mono 16-bit PCM. The client packages it as WAV and owns
and revokes each playback object URL. A bounded client cache and four-passage rolling lookahead
reduce gaps. Play and explicit seek prepare the current and next two passages before
playback; automatic passage transitions refill without another startup wait. At most
two requests run at once, including obsolete requests still finishing. Pause, seek
and voice changes suppress queued work for the old position; submitted requests may
finish into cache. Failed lookahead requests cool down rather than retrying immediately. No synthesis happens until Play; loading the catalogue does not generate
speech. Requests already submitted may finish into cache after Pause or navigation.
Cache is process-local, not a durable generated-audio library or exactly-once guarantee
across Hub restarts. Audio errors pause with Retry and Reload controls.

The reader has a separate CSP allowing same-origin scripts/connections and blob audio.
It does not relax the authored-document iframe policy. Entry links bypass PJAX so audio
has a normal document lifecycle and cannot continue in a discarded page.

## Verification

- `node scripts/check-listen-buffer.cjs` deterministically delays synthesis to verify
  startup buffering, rolling refill, immediate cached transitions, two-request limit,
  cancellation after pause/seek/voice change, and failed-prefetch cooldown.

- `go test ./internal/hub -run '^TestListen'` exercises source fidelity, chunks,
  read-only access, source-version checks, origin checks, non-source speech rejection,
  voice-sensitive caching, pending deduplication, concurrency and malformed audio.
- `go test -race ./internal/hub -run '^TestListen'` checks the gateway's concurrent state.
- `node scripts/check-listen-browser.cjs` runs an isolated Go test Hub with simulated
  PCM and a fresh Chrome context, then checks controls, voice changes, queue progression,
  saved position/queue, pause during pending audio and responsive widths. It requires
  `playwright` in Node's module path and Chrome installed (`CHROME_CHANNEL` overrides
  the default channel). Screenshots are written to the reported temporary directory.
- The fixture is opt-in via `AFS_LISTEN_BROWSER_FIXTURE`; normal Go tests skip it.
- Run a small authenticated live synthesis before release. Local mocked audio verifies
  player behavior, not Gemini availability or audible quality.
