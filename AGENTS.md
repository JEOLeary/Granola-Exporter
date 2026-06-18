# Granola-Exporter — Agent Guide

## Entrypoint

`cmd/granola-backup/main.go` — CLI flag parsing, fallback orchestration. Config at `cmd/granola-backup/config.go` (YAML).

## Build

```powershell
# Windows
go build -o bin\granola-backup.exe .\cmd\granola-backup
# macOS/Linux
go build -o bin/granola-backup ./cmd/granola-backup
# Cross-compile (from any OS):
GOOS=linux GOARCH=amd64 go build -o bin/granola-backup-linux ./cmd/granola-backup
GOOS=darwin GOARCH=amd64 go build -o bin/granola-backup-darwin ./cmd/granola-backup
# Docker
docker compose build
```

## Test

```powershell
go test ./...                          # all tests
go test -v ./internal/api/            # API package tests
go test -v ./internal/output/         # Output package tests
go test -run TestExtractClientID ./internal/api/  # single test
```

Tests use `httptest.NewServer` for HTTP mocking. No external services needed. No integration test prerequisites.

## Module

`github.com/granola-exporter/granola-backup` — Go 1.24.5. Two external deps: `gorilla/websocket` (CDP), `gopkg.in/yaml.v3` (config).

## Packages

```
cmd/granola-backup/          CLI entry point, flag parsing, fallback chain
internal/
  api/                       Granola Private REST API client (WorkOS refresh, ProseMirror→MD)
  cdp/                       Chrome DevTools Protocol token extraction (launches Granola with --remote-debugging-port)
  encryptedjson/             Chromium DPAPI/Keychain + AES-GCM decryption
  output/                    Meeting model, manifest (exported.json), go text/template rendering
  redact/                    JWT/token redaction utility
```

## Key CLI flags

| Flag | Default | Note |
|------|---------|------|
| `-output` | `Granola.ai` | |
| `-since` | `""` | `YYYY-MM-DD` or RFC3339. Overrides `-days`. |
| `-days` | `365` | Ignored if `-since` set. |
| `-export-all` | `false` | Ignores manifest, implies `-overwrite`. |
| `-refresh-token-file` | `""` | Skips all credential fallback, uses WorkOS refresh directly. |
| `-extract-token-only` | `""` | Writes `refresh_token.json` and exits (must run on Granola machine). |
| `-config` | `granola-backup.yaml` | Searches `granola-backup.yaml` then `granola-backup.yml` in CWD. |
| `-exclude` | `""` | Comma-separated folder names, case-insensitive matching. |

## Fallback chain

1. File-based (`supabase.json.enc` → DPAPI/Keychain → CredMan on Windows)
2. CDP (launches Granola with `--remote-debugging-port`, calls `electron.ipcInvoke('get-session')`)
3. Direct WorkOS refresh via `-refresh-token-file` (skips all above)

## Config file

`granola-backup.yaml` in CWD (or `-config` flag). CLI flags override YAML values.

## Platform specifics

| OS | Features |
|----|----------|
| Windows | CDP, DPAPI decryption, Credential Manager, refresh token extraction |
| macOS | CDP, Keychain decryption, refresh token extraction |
| Linux | Export only (needs `-refresh-token-file` from another machine) |

- DPAPI is machine+user-bound. Encrypted files cannot be decrypted on a different machine.
- Extracting tokens requires the Granola desktop app on a machine where it's been used.

## Output

Files: `(YYYY-MM-DD HH-MM-SS) Title.md` organized by folder. `exported.json` for dedup/incremental. `meeting_template.md` + `transcript_segment_template.md` for customizable rendering (Go `text/template`). Templates walk up directory tree looking for the respective file.

Meetings in multiple folders: primary → hardlink → symlink → copy in additional folders.

## Committing

- Do NOT commit personal data (meeting notes, transcripts, tokens, credentials, `refresh_token.json`, `exported.json`, `Granola.ai/` contents, decryption keys, file paths containing usernames, or any machine-specific paths/info) unless explicitly instructed by the user.

## Test quirks

- No CI/CD workflows.
- No Makefile, Taskfile, or Justfile.
- Tests mock API endpoints — no real Granola instance needed.
- `TestWriteMeetingIntegration` tests dedup via manifest.
- Manifest backward compat tested with old JSON format (`meeting_date` field).

<!-- BEGIN BEADS INTEGRATION v:1 profile:full hash:0a1bbe8a -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Dolt-powered version control with native sync
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update <id> --claim --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task atomically**: `bd update <id> --claim`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Quality
- Use `--acceptance` and `--design` fields when creating issues
- Use `--validate` to check description completeness

### Lifecycle
- `bd defer <id>` / `bd supersede <id>` for issue management
- `bd stale` / `bd orphans` / `bd lint` for hygiene
- `bd human <id>` to flag for human decisions
- `bd formula list` / `bd mol pour <name>` for structured workflows

### Auto-Sync

bd automatically syncs via Dolt:

- Each write auto-commits to Dolt history
- No manual export/import needed!

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

<!-- END BEADS INTEGRATION -->
