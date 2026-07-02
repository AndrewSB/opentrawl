---
written_by: ai
---

# Roadmap

Companion to [vision.md](vision.md). Phases overlap; the contract leads,
per-crawler hardening runs in parallel behind it.

## Decisions already made

- Hybrid ownership: fork only where blocked. crawlkit, imsgcrawl and
  photoscrawl sync directly with openclaw (maintainer access); telecrawl,
  wacrawl and clawdex sync through joshp123 forks; the Gmail, Calendar,
  Apple Notes and Signal crawlers are monorepo-native.
- One monorepo under the opentrawl org, open source (MIT), with
  attribution. Crawler directories are git subtrees; `scripts/sync`
  moves changes both ways.
- The Mac app is built from scratch in SwiftUI, minimum macOS one below
  current. Upstream crawlbar is a cherry-pick source (its control
  protocol doc and quality rubric are worth keeping; its settings UI is
  not).
- Federated architecture: per-source databases, one `trawl` CLI on top.
  No shared schema.
- Agent first, human readable, local first, no knobs, read only in v1.
- Privacy split: this public monorepo carries code and public docs only.
  Private working context stays out; `scripts/check-clean` enforces the
  mechanical part in CI.

## Phase 0: monorepo and hygiene

Mostly done at creation (2026-07-02): subtree imports with full history
for the six existing crawlers, sync script, privacy check in CI.

Remaining:

- Gmail and Calendar crawlers restart clean in the monorepo: the
  private prototypes' logic migrates by rewrite, not by history import.
- retire the local crawlbar PR worktrees after confirming their content
  landed upstream.

## Phase 1: contract v1

Goal: the control contract is written down, versioned and testable, so
crawler work and app work can proceed against it independently.

- extend the crawlkit control contract to cover: `metadata`, `status`,
  `sync`, `search`, `open`, `doctor`, `contacts export`; bounded and
  paginated output rules; the secrets rule (booleans and expiry only);
  human-shaped output rules (real timestamps, human names for IDs);
  error and progress shapes.
- define the golden-path bar as a checklist derived from the crawler
  quality rubric and the imsgcrawl evaluation, so "done" is mechanical
  per crawler.
- build a conformance harness: point it at any crawler binary and it
  verifies the contract (shapes, bounds, secret leaks, empty and corrupt
  archive behaviour). This is what makes the plugin story real.
- push contract work upstream to crawlkit as we go.

## Phase 2: per-crawler hardening to the bar

Goal: every v1 crawler passes the conformance harness. Highly parallel.

| crawler | state | main gaps |
|---|---|---|
| imsgcrawl | golden path: archive with source parity, FTS proven | installed binary, human-shaped IDs and timestamps, attachment handling |
| telecrawl | works: archive and media proven | metadata drift, status envelope |
| wacrawl | archive works, readiness unproven | readiness proof, stop auto-sync on read, status envelope; watch WhatsApp passkey pairing risk |
| gogcrawl (new) | private prototype exists | rebuild clean in monorepo: real archive CLI over a mail mirror, config-driven paths |
| calcrawl (new) | private scaffold exists | rebuild clean in monorepo: Google and ICS sync, archive, search |
| clawdex | contact layer works | adopt the crawlkit contact-export contract, contract compliance, import loop from all v1 crawlers |

Contact export is a v1 requirement for every crawler, because identity
joins are the horizon and re-crawling later is the expensive path.

## Phase 3: the federation CLI

Goal: one binary, one surface. `trawl status`, `trawl sync <source>`,
`trawl search <query> --source a,b`, `trawl open <ref>`, `trawl doctor`.

- new Go binary in the monorepo. Start from crawlkit's `crawlctl`
  (discovery, run locking, history) and keep the contract as the only
  coupling to crawlers.
- search federates across per-source FTS; output interleaves sources
  with provenance on every row.
- design hook for v2 deltas: sync cursors already exist in crawlkit, so
  `trawl diff --since 24h` is a federation feature later, not
  per-crawler work.

## Phase 4: the Mac app

Goal: a consumer-grade menu bar app a human likes and trusts. Shows the
key metrics per crawler (fresh or stale, counts, last sync, auth state),
handles authorisation flows, runs syncs. No settings maze.

- from scratch, SwiftUI, SwiftPM without an Xcode project, minimum
  macOS one below current.
- keep upstream crawlbar's quality rubric as the review contract for the
  app, and its control protocol as the compatibility line so upstream
  crawlers work unchanged.
- every UI change ships with before and after visual proof.

## v1.5 and v2

- v1.5: Apple Notes. Recovering note history is proven feasible
  (current-body decoding, WAL replay, snapshot and backup recovery); the
  extractor lands here when it works.
- v2: Signal spike (Signal Desktop keeps an SQLCipher database with the
  key in the OS keychain; assess a snapshot approach before committing),
  Photos, X, daily deltas, write capability through the upstream access
  CLIs, published plugin API, MCP or Executor adapter.

## Way of working

- adversarial review on every substantial change: independent reviewers
  told to refute, not confirm.
- all prose (docs, PRs, commits) follows plain-language style. Code must
  read as self-documenting or it does not merge.
- verification over assertion: a crawler change is done when the
  conformance harness passes against a real archive, not when it builds.
