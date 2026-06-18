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

Files: `(YYYY-MM-DD HH-MM-SS) Title.md` organized by folder. `exported.json` for dedup/incremental. `template.md` for customizable rendering (Go `text/template`). Template walks up directory tree looking for `template.md`.

Meetings in multiple folders: primary → hardlink → symlink → copy in additional folders.

## Committing

- Do NOT commit personal data (meeting notes, transcripts, tokens, credentials, `refresh_token.json`, `exported.json`, `Granola.ai/` contents, decryption keys, file paths containing usernames, or any machine-specific paths/info) unless explicitly instructed by the user.

## Test quirks

- No CI/CD workflows.
- No Makefile, Taskfile, or Justfile.
- Tests mock API endpoints — no real Granola instance needed.
- `TestWriteMeetingIntegration` tests dedup via manifest.
- Manifest backward compat tested with old JSON format (`meeting_date` field).

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:7510c1e2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

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
