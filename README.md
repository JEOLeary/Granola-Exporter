# Granola-Exporter

Export meeting notes and transcripts from [Granola.ai](https://granola.ai) to local markdown files, organized by folder assignment. Works without a paid Granola subscription by reusing the desktop app's existing session.

## Quick Start

```bash
# On a machine with Granola installed (extracts token automatically):
granola-backup

# Export only recent meetings:
granola-backup -since-last-export

# Export meetings from a specific date range:
granola-backup -since 2026-01-01 -days 30

# Output to a custom location:
granola-backup -output ~/Documents/Meetings

# Exclude meetings from specific folders:
granola-backup -exclude "Personal,Brag Docs"
```

## Project Structure

```
cmd/granola-backup/      # CLI entry point, flag parsing, fallback orchestration
internal/
  config/                # YAML config loading
  redact/                # JWT/token redaction
  encryptedjson/         # DPAPI / macOS Keychain decryption
  api/                   # Granola Private REST API client
  output/                # Meeting model, manifest, markdown templating
  mcp/                   # MCP JSON-RPC client (OAuth device flow)
  cdp/                   # Chrome DevTools Protocol automation
  sqlcipher/             # SQLCipher database extraction
```

## Usage

```
granola-backup [flags]
```

### Flags

| Flag                 | Default                | Description                                                                                                            |
| -------------------- | ---------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `-output`            | `Granola.ai`           | Output directory for markdown files                                                                                    |
| `-days`              | `365`                  | Days of history to fetch (ignored if `-since` or `-since-last-export` is set)                                          |
| `-since`             | `""`                   | Fetch meetings created after this date (`YYYY-MM-DD` or RFC3339). Takes priority over `-days` and `-since-last-export` |
| `-since-last-export` | `false`                | Only fetch meetings since the latest exported file                                                                     |
| `-overwrite`         | `false`                | Overwrite existing files (default: skip)                                                                               |
| `-debug`             | `false`                | Enable debug logging (dumps raw API fields for diagnostics)                                                            |
| `-token-file`        | `./granola_token.json` | MCP OAuth token file path                                                                                              |
| `-node-path`         | `""`                   | Path to Node.js 24+ executable (for SQLCipher extraction)                                                              |
| `-exclude`           | `""`                   | Comma-separated folder names to exclude (e.g. `-exclude "Personal,Brag Docs"`)                                        |
| `-config`            | `granola-backup.yaml`  | YAML config file for persistent flag values (searches `granola-backup.yaml` then `granola-backup.yml`)                |
| `-granola-path`      | _(auto-detected)_      | Granola executable path                                                                                                |

### Date Filtering

The three date flags interact in priority order:

1. `-since` — explicit date, highest priority
2. `-since-last-export` — computes date from manifest or filename scan
3. `-days` — relative range, lowest priority (default: 365)

If `-since-last-export` is used but no previous exports exist, it falls back to `-days`.

## Config File

Create `granola-backup.yaml` in the current directory to set persistent defaults. CLI flags override config file values.

```yaml
output: "Granola.ai"
days: 365
since: ""
since-last-export: false
overwrite: false
debug: false
token-file: ""
node-path: ""
granola-path: "C:\\Users\\JEOLeary\\AppData\\Local\\Programs\\granola\\Granola.exe"
exclude: "Personal,Brag Docs"
```

Use a custom path with the `-config` flag:

```bash
granola-backup -config /path/to/config.yaml
```

## Output Format

### Directory Structure

```
Granola.ai/
  template.md                      # customizable output template (auto-created)
  exported.json                    # dedup manifest
  Personal/
    (2026-01-15 16-16-01) Title.md
    (2026-02-02 15-05-18) Title.md
  Voya/
    (2026-03-10 10-30-00) Title.md
    (2026-04-09 19-38-59) Title.md
```

### File Content

Each meeting file is rendered from `template.md` using Go's `text/template` engine. A default template is auto-created in the output directory on the first run:

```markdown
# {{.Title}}

Date/Time: {{.DateTimeFormatted}}

Meeting ID: {{.MeetingID}}

## Notes

{{if .Notes}}{{.Notes}}{{else}}_No notes_{{end}}

---

## Transcript

{{if .Transcript}}{{.Transcript}}{{else}}_No transcript_{{end}}
```

The default template produces files like:

```markdown
# Meeting Title

Date/Time: Mon Jan 15 16:16:01 2026

Meeting ID: 00dd79a5-5d63-4aec-8d5f-bd50e4766e4d

## Notes

Meeting notes content...

---

## Transcript

Speaker 1: First line of transcript.

Speaker 2: Response line.
```

### Customizing the Template

Edit `template.md` in the output directory to change how meetings are formatted. Template variables:

| Variable                 | Description                                      |
| ------------------------ | ------------------------------------------------ |
| `{{.Title}}`             | Meeting title                                    |
| `{{.DateTimeFormatted}}` | Date/time formatted as `Mon Jan 2 15:04:05 2006` |
| `{{.MeetingID}}`         | Unique meeting identifier                        |
| `{{.Notes}}`             | Meeting notes content (or empty string)          |
| `{{.Transcript}}`        | Meeting transcript (or empty string)             |

**Conditionals** hide sections when a variable is empty:

```
{{if .Notes}}{{.Notes}}{{else}}*No notes*{{end}}
```

The template is read from disk every run. The binary searches from the file's directory upward to the output root, so you can use different templates per folder.

### Folder Organization

Meetings placed in Granola "lists" (folders) are organized into subdirectories. If a meeting belongs to multiple lists, the file goes in the first list's directory (sorted alphabetically by list title), and hardlinks (falling back to symlinks, then copies) are created in the other list directories.

Transcript timestamps are removed when every segment has the same timestamp value (the common case).

## Requirements

### Supported OSes

| OS          | Status  | Notes                                                      |
| ----------- | ------- | ---------------------------------------------------------- |
| **Windows** | Full    | CDP token extraction, DPAPI decryption, Credential Manager |
| **macOS**   | Partial | CDP token extraction, Keychain decryption                  |
| **Linux**   | Partial | MCP auth only (no Granola desktop, no encrypted credential files) |

### Dependencies

- **Go 1.24+** (to build from source)
- **PowerShell** (Windows DPAPI operations, always present on Windows)
- **Granola desktop app** (for CDP-based token extraction; optional if you have file-based credentials)

Build from source with `go build ./cmd/granola-backup`.

## Architecture

Granola-Exporter uses a **fallback chain** of extraction methods, each tried in order until one succeeds:

```
MCP API (OAuth device flow)
  → CDP token + Private API (launches Granola with --remote-debugging-port)
    → File-based credentials + Private API
      → supabase.json.enc (DPAPI on Windows, Keychain on macOS)
      → supabase.json.enc.bak (encrypted backup)
      → Windows Credential Manager (cross-keychain format)
    → SQLCipher database extraction (Node.js + better-sqlite3-multiple-ciphers)
      → CDP DOM extraction (browser automation fallback)
```

### Method Details

#### 1. MCP API (`internal/mcp/`)

Granola's Model Context Protocol server. Uses OAuth2 device authorization flow. Requires one-time browser-based authentication. Produces a `granola_token.json` file.

#### 2. CDP Token + Private API (`internal/cdp/` + `internal/api/`)

Launches Granola with `--remote-debugging-port=9222`, connects via Chrome DevTools Protocol, and calls `window.electron.ipcInvoke('get-session')` to extract the live JWT. Uses this token with Granola's private REST API at `api.granola.ai`. This is the primary path on Windows/macOS — it works automatically when Granola is installed.

#### 3. File-based Credentials + Private API (`internal/api/` + `internal/encryptedjson/`)

Reads WorkOS session tokens from Granola's on-disk credential files. Supports these sources:

| Source                  | Format                   | Platform                      |
| ----------------------- | ------------------------ | ----------------------------- |
| `supabase.json.enc`     | DPAPI / Keychain AES-GCM | Windows DPAPI, macOS Keychain |
| `supabase.json.enc.bak` | Encrypted backup         | Same as above                 |
| Windows CredMan         | `cross-keychain` format  | Windows                       |

Once credentials are obtained, WorkOS token refresh is attempted if the access token is expired. The Private API provides ProseMirror-formatted notes (with fallbacks: ProseMirror `notes` → `last_viewed_panel.content` HTML-to-markdown), dedicated transcript endpoint, and document-to-list (folder) mappings. If the list endpoint returns empty notes, a per-document detail fetch (`POST /v1/get-document`) is tried as a second pass.

#### 4. SQLCipher Database (`internal/sqlcipher/`)

Decrypts `granola.db` using Granola's Data Encryption Key (DEK) from `storage.dek`. The DEK chain on Windows:

```
Local State → DPAPI CryptUnprotectData → 32-byte AES key
                                               ↓
storage.dek → AES-256-GCM decrypt → base64 DEK → 32-byte SQLCipher key
                                               ↓
granola.db → PRAGMA key = "x'<hex>'" with cipher=sqlcipher, legacy=4
```

**Status:** Key extraction is verified working, but the `better-sqlite3-multiple-ciphers` native addon has NODE_MODULE_VERSION incompatibility (Electron 146 vs Node 24 137). The database schema does not contain a `document_folders` table, so folder mapping is not available from this path.

#### 5. CDP DOM Extraction (`internal/cdp/`)

Last-resort browser automation. Launches Granola with CDP, scrapes visible text from the meeting detail view, and falls back to heap memory search for note data objects.

### Token Refresh

WorkOS refresh tokens are used to obtain fresh access tokens:

```
POST https://api.workos.com/user_management/authenticate
Content-Type: application/json

{
  "client_id": "<extracted from JWT iss claim>",
  "grant_type": "refresh_token",
  "refresh_token": "<refresh_token>"
}
```

The `client_id` is extracted from the JWT's `iss` claim (e.g., `client_01JZJ0XB...`) or defaults to `client_GranolaMac`.

### Dedup and Incremental Export

Each export run creates/updates `exported.json` in the output directory:

```json
{
	"version": 1,
	"exported": [
		{
			"id": "00dd79a5-5d63-4aec-8d5f-bd50e4766e4d",
			"path": "Voya/(2026-06-12 16-16-01) Title.md",
			"exported_at": "2026-06-13T10:00:00Z",
			"meeting_datetime": "2026-06-12T16:16:01Z"
		}
	]
}
```

Documents already in the manifest are skipped (unless `-overwrite` is set). The API path sorts documents by `CreatedAt` descending and stops at the first already-exported document.

## Comparison to Similar Tools

| Feature                   | Granola-Exporter                       | graincrawl                         | theantichris/granola | magarcia/granola-cli     |
| ------------------------- | -------------------------------------- | ---------------------------------- | -------------------- | ------------------------ |
| **Auth: CDP token**       | Yes                                    | No                                 | No                   | No                       |
| **Auth: File-based** | supabase.json.enc, Windows CredMan | supabase.json/enc (macOS Keychain) | supabase.json | supabase.json → Keychain |
| **Auth: Windows CredMan** | Yes                                    | No                                 | No                   | No                       |
| **Auth: MCP OAuth**       | Yes                                    | No                                 | No                   | No                       |
| **Token refresh**         | WorkOS                                 | No                                 | No                   | WorkOS (with lock)       |
| **Encrypted files**       | DPAPI + macOS Keychain                 | macOS Keychain only                | No                   | No                       |
| **Export format**         | Markdown files by folder               | CLI output / files                 | CLI output files     | CLI output               |
| **Folder organization**   | Yes (lists API)                        | Yes (lists API)                    | No                   | No                       |
| **Transcript support**    | Dedicated endpoint                     | No                                 | Cache file only      | No                       |
| **Headless/remote**       | Yes (file/MCP)                         | Yes                                | Yes                  | Yes                      |

## Suggested Improvements

### Decryption

- **Investigate Windows `supabase.json.enc` format**: On some machines, the file uses an unknown encryption format (not raw DPAPI, not Chromium OSCrypt v10). Likely wrapped in IPC framing from the SUPABASE_STORAGE_PROCESS child process.
- **macOS Keychain verification**: The AES-GCM decryption path for macOS (`internal/encryptedjson/`) follows the standard Electron safeStorage format but has not been tested on actual macOS hardware.
- **SQLCipher key mismatch**: The extracted DEK does not open `granola.db` despite verified key derivation. Possible causes: database rekey, version mismatch, or different SQLCipher parameters than declared (`legacy = 4`).

### Features

- **Notifications**: Slack/email/webhook on completion or failure.


### Code Quality

- **Rate limiting**: The Private API client has basic retry but no adaptive rate limiting.

## Glossary

| Term                               | Description                                                                                                                                                                                       |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **CDP** (Chrome DevTools Protocol) | Protocol used to inspect and control Chromium-based browsers. Granola-Exporter launches Granola with `--remote-debugging-port` and uses CDP to extract the session JWT from the renderer process. |
| **DPAPI** (Data Protection API)    | Windows encryption service that protects data at rest using the logged-in user's credentials. Used to decrypt Granola's `supabase.json.enc` credential file.                                      |
| **MCP** (Model Context Protocol)   | Granola's JSON-RPC API server used by AI assistants. Granola-Exporter uses it as a fallback auth path via OAuth2 device authorization flow.                                                       |
| **ProseMirror**                    | Rich text editor framework. Granola stores notes as ProseMirror JSON documents; the exporter converts them to markdown.                                                                           |
| **WorkOS**                         | Identity platform that provides Granola's authentication (login, token issuance, refresh). The exporter uses WorkOS refresh tokens to obtain fresh access tokens.                                 |
| **SQLCipher**                      | SQLite extension that provides transparent 256-bit AES encryption. Granola's local database (`granola.db`) uses SQLCipher.                                                                        |
| **JWT** (JSON Web Token)           | Auth token used to authenticate API requests. Granola-Exporter extracts the JWT from the desktop app's session or from credential files.                                                          |
| **REST API**                       | Granola's private HTTP API at `api.granola.ai` — endpoints for documents, transcripts, and folder metadata. The primary data source for exports.                                                  |

## Scheduled Export (Linux systemd)

Set up automatic daily exports using systemd:

**`/etc/systemd/system/granola-backup.service`**

```ini
[Unit]
Description=Granola meeting export
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/granola-backup -output /var/backups/granola -since-last-export
User=backup
```

**`/etc/systemd/system/granola-backup.timer`**

```ini
[Unit]
Description=Daily Granola export

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable granola-backup.timer
sudo systemctl start granola-backup.timer
```

Check logs with `journalctl -u granola-backup.service`.

## Building from Source

```bash
# Windows
go build -o bin/granola-backup.exe ./cmd/granola-backup

# Linux (cross-compile)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/granola-backup-linux ./cmd/granola-backup

# macOS (cross-compile)
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o bin/granola-backup-darwin ./cmd/granola-backup
```

## License

MIT
