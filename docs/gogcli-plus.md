# gogcli-plus: Drive Sync + Contacts Cleanup

## Current Architecture (as-is)
- `cmd/gog/main.go` is the CLI entrypoint; all behavior lives in `internal/cmd`.
- `internal/cmd` uses Kong for commands/flags and delegates to service clients.
- `internal/googleapi` builds API clients and HTTP retry/circuit breaker layers.
- `internal/googleauth` handles OAuth flows, scopes, and service accounts.
- `internal/config` and `internal/secrets` store config and tokens.
- `internal/outfmt` and `internal/ui` standardize JSON/plain/table output.

## Proposed Architecture (additions)
- **Drive reporting layer**: reusable tree/list helpers that emit `[]DriveItem` with `path`, `id`, `mimeType`, `size`, and timestamps.
- **Drive sync engine**: plan/build/apply flow with pure functions for diffing and side-effectful executors (download/upload/delete).
- **Contacts dedupe engine**: normalization + grouping + merge planning, with a safe preview/apply workflow.
- **State/config**: optional local sync state file for drive sync defaults; no secrets stored.

## PRD
### Goals
- Add Drive reporting commands: `drive tree`, `drive du`, and a compact inventory report.
- Add Drive sync (pull + push) with dry-run, filters, and optional deletes.
- Add Contacts dedupe with preview, merge plan, and apply mode.
- Preserve existing output modes (`--json`, `--plain`, human tables).

### Non-goals
- Two-way conflict resolution beyond simple “newer wins”.
- Full offline index of Drive/Contacts.
- Automatic conversion of local files into native Google Docs formats (v1).

### Users & Use Cases
- Power users needing quick Drive storage insight and cleanup.
- Teams syncing a Drive folder to local (and back) for backups or workflows.
- Users with duplicated contacts from multiple imports or devices.

### Functional Requirements
- `drive tree`: recursive listing with depth limit, path output, and size/modified metadata.
- `drive du`: aggregated sizes per folder with sorting and depth control.
- `drive inventory`: flat report with id, path, owner, size, and modified time.
- `drive sync pull`: Drive → local sync with optional `--delete` mirror.
- `drive sync push`: local → Drive sync with optional `--delete` mirror.
- Sync includes `--dry-run`, `--exclude`, and `--include` filters.
- Contacts dedupe supports preview (default) and apply mode with confirmation.
- Dedupe matching defaults to `email,phone,name` with a `--match` override.

### Constraints
- Respect Google API rate limits (use existing retry transport).
- Keep stdout parseable; hints/progress to stderr.
- Avoid storing secrets in sync state files.

## Roadmap
### Epic 1: Drive reporting
- [x] Add tree inventory helpers (path + metadata)
- [x] Implement `gog drive tree`
- [x] Implement `gog drive du`
- [x] Implement `gog drive inventory`

### Epic 2: Drive sync
- [x] Define sync plan structures and diffing helpers
- [x] Implement `gog drive sync pull`
- [x] Implement `gog drive sync push`
- [x] Add filters, dry-run, delete safeguards, and summaries

### Epic 3: Contacts cleanup
- [x] Add normalization and grouping helpers
- [x] Implement `gog contacts dedupe` (preview + apply)
- [x] Add merge plan output for JSON/plain/table modes

### Epic 4: Tests & docs
- [x] Unit tests for sync diffing and path sanitization
- [x] Unit tests for contact grouping/merge logic
- [x] Update README command examples

## Test Plan
- Drive: pure-function unit tests for path building, filters, and sync plan diffs.
- Contacts: unit tests for normalization, grouping, and merge selection.
- CLI: command validation tests for new flags and modes.
