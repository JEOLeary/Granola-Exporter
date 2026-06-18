# Granola-Exporter

Export meeting notes and transcripts from [Granola.ai](https://granola.ai) to local markdown files, organized by folder assignment. Works without a paid Granola subscription by reusing the desktop app's existing session.

## Building

```console
# Windows
go build -o bin\granola-backup.exe .\cmd\granola-backup

# macOS / Linux
go build -o bin/granola-backup ./cmd/granola-backup

# Cross-compile (from any OS):
GOOS=linux GOARCH=amd64 go build -o bin/granola-backup-linux ./cmd/granola-backup
GOOS=darwin GOARCH=amd64 go build -o bin/granola-backup-darwin ./cmd/granola-backup
```

## Quick Start

```console
# On a machine with Granola installed (incremental since last export):
granola-backup

# Export all meetings (ignore manifest, overwrite existing):
granola-backup -export-all

# Export on a machine without Granola (uses a pre-extracted refresh token):
granola-backup -refresh-token-file refresh_token.json

# Extract a refresh token from a Granola install for use on another machine:
granola-backup -extract-token-only refresh_token.json

# Export meetings from a specific date range (30 days starting 01/01/2026):
granola-backup -since 2026-01-01 -days 30

# Output to a custom location:
granola-backup -output ~/Documents/Meetings

# Exclude meetings from specific folders:
granola-backup -exclude "Personal,Brag Docs"
```

## Two-Machine Workflow

Extract a long-lived refresh token from a machine with Granola, then export meetings from any machine (no Granola install needed).

### Step 1: Extract (on a machine with Granola)

```console
granola-backup -extract-token-only refresh_token.json
```

This produces `refresh_token.json` containing `refresh_token` and `client_id`. It uses the same credential extraction as the main export (encrypted file → Windows Credential Manager), writes the token file, and exits without exporting. The tool decrypts `supabase.json.enc` from `%APPDATA%\Granola\` using the Chromium keychain (Local State DPAPI → storage.dek AES-GCM).

**Must run on the actual machine with Granola installed.** The decryption process uses DPAPI, which is machine-bound — the `os_crypt.encrypted_key` in `Local State` can only be decrypted on the same hardware + Windows user account that created it. Copying the files to another machine and pointing `--file` at a UNC path will fail at the DPAPI step.

Remote execution of `granola-backup -extract-token-only` is possible via PowerShell Remoting (WinRM), but setting that up is outside the scope of this guide.

A single `refresh_token.json` works for **all workspaces** tied to your Granola account.

### Step 2: Copy

Copy `refresh_token.json` to the target machine (USB, SCP, sync, etc). It is a plain JSON file — protect it like a password.

### Step 3: Export (any machine, no Granola)

```console
granola-backup -refresh-token-file refresh_token.json
```

The `-refresh-token-file` flag skips the entire fallback chain (CDP, file-based). It reads `refresh_token` + `client_id`, calls the WorkOS refresh endpoint for a fresh `access_token`, then exports via the Private API.

### Refresh Token Lifespan

WorkOS refresh tokens are **long-lived but not indefinite**. Observations:

| Aspect | Typical Value |
| - | - |
| Access token TTL | ~6 hours (`expires_in: 21599` seconds) — auto-refreshed by `-refresh-token-file` on each run |
| Refresh token TTL | Not publicly documented by WorkOS. Observed to persist months across Granola app reinstalls. |
| Invalidation triggers | Password change, session revocation in WorkOS admin, account deletion, token rotation on re-auth |

**The refresh token does not expire while you use it regularly.** Each successful export extends its useful life. If the refresh token does expire, re-run Step 1 on the Granola machine to produce a fresh `refresh_token.json`.

### Known Issues

- **Granola re-auth invalidates old tokens**: If you sign out of Granola and sign back in, the old refresh token is invalidated. Extract a fresh one.
- **Clock skew**: The WorkOS refresh endpoint checks timestamps. Ensure both machines have reasonably accurate system clocks.
- **Cross-platform**: `granola-backup -extract-token-only` works on Windows and macOS. The `refresh_token.json` works on any OS — Windows, macOS, or Linux.

## Project Structure

```text
cmd/granola-backup/      # CLI entry point, flag parsing, fallback orchestration
internal/
  redact/                # JWT/token redaction
  encryptedjson/         # Chromium keychain + AES-GCM decryption
  api/                   # Granola Private REST API client
  output/                # Meeting model, manifest, markdown templating
  cdp/                   # Chrome DevTools Protocol token extraction
```

## Usage

```console
granola-backup [flags]
```

### Flags

| Flag | Default | Description |
| - | - | - |
| `-output` | `Granola.ai` | Output directory for markdown |
| `-days` | `365` | Days of history to fetch (ignored if `-since` is set) |
| `-since` | `""` | Fetch meetings created after this date (`YYYY-MM-DD` or RFC3339). Takes priority over `-days` |
| `-export-all` | `false` | Export all meetings, ignoring last-export manifest (implies `-overwrite`) |
| `-overwrite` | `false` | Overwrite existing files |
| `-debug` | `false` | Enable debug logging (dumps raw API fields for diagnostics) |
| `-exclude` | `""` | Comma-separated folder names to exclude (e.g. `-exclude "Personal,Brag Docs"`) |
| `-refresh-token-file` | `""` | Path to `refresh_token.json` from `granola-backup -extract-token-only`. Skips all fallback methods, uses WorkOS refresh directly |
| `-extract-token-only` | `""` | Extract `refresh_token.json` and exit (no export). Useful for producing a token on a Granola machine for use on another machine |
| `-config` | `granola-backup.yaml` | YAML config file for persistent flag values (searches `granola-backup.yaml` then `granola-backup.yml`) |
| `-granola-path` | _(auto-detected)_ | Granola executable path |

### Date Filtering

By default, only meetings since the last export are fetched (tracked via `exported.json`). Two flags override this:

1. `-since` — explicit date, highest priority
2. `-export-all` — exports everything, ignoring the manifest
3. `-days` — relative range, lowest priority (default: 365)

The manifest date is used when no flags are given. If no previous export exists, it falls back to `-days`.

## Config File

Create `granola-backup.yaml` in the current directory to set persistent defaults. CLI flags override config file values.

```yaml
output: "Granola.ai"
days: 365
since: ""
overwrite: false
debug: false
granola-path: "C:\\Users\\{USERNAME}\\AppData\\Local\\Programs\\granola\\Granola.exe"
exclude: "Personal,Brag Docs"
refresh-token-file: ""
```

Use a custom path with the `-config` flag:

```console
granola-backup -config /path/to/config.yaml
```

## Output Format

### Directory Structure

```text
Granola.ai/
  meeting_template.md              # customizable meeting template (auto-created)
  transcript_segment_template.md   # customizable transcript segment template (auto-created)
  exported.json                    # dedup manifest
  Personal/
    (2026-01-15 16-16-01) Title.md
    (2026-02-02 15-05-18) Title.md
  Voya/
    (2026-03-10 10-30-00) Title.md
    (2026-04-09 19-38-59) Title.md
```

### File Content

Each meeting file is rendered from two templates using Go's `text/template` engine:

1. **`meeting_template.md`** — the per-meeting layout (title, notes, transcript section)
2. **`transcript_segment_template.md`** — formatting for each individual transcript segment (speaker, text, timestamps)

Defaults are auto-created in the output directory on the first run:

**Default `meeting_template.md`:**

```markdown
# {{.Meeting.Title}}

Date/Time: {{.Meeting.StartDateTimeFormatted}}
{{if .Meeting.EndDateTimeFormatted}}End Time: {{.Meeting.EndDateTimeFormatted}}{{end}}
{{if .Meeting.DurationFormatted}}Duration: {{.Meeting.DurationFormatted}}{{end}}
Meeting ID: {{.Meeting.ID}}

## Notes

{{if .Meeting.Notes}}{{.Meeting.Notes}}{{else}}*No notes*{{end}}

---

## Transcript

{{if .Meeting.Transcript}}{{.Meeting.Transcript}}{{else}}*No transcript*{{end}}
```

**Default `transcript_segment_template.md`:**

```text
{{.TranscriptSegment.Speaker}}: {{.TranscriptSegment.Text}}
```

The rendered meeting files look like:

```markdown
# Meeting Title

Date/Time: Monday January 15, 2026 4:16 PM
End Time: Monday January 15, 2026 5:30 PM
Duration: 1h 14m
Meeting ID: 00dd79a5-5d63-4aec-8d5f-bd50e4766e4d

## Notes

Meeting notes content...

---

## Transcript

Speaker 1: First line of transcript.

Speaker 2: Response line.
```

### Customizing the Meeting Template

Edit `meeting_template.md` in the output directory to change how meetings are formatted. Template variables:

| Variable | Description |
| - | - |
| `{{.Meeting.Title}}` | Meeting title |
| `{{.Meeting.StartDateTimeFormatted}}` | Meeting start time in local timezone, formatted as `Monday January 2, 2006 3:04 PM` |
| `{{.Meeting.EndDateTimeFormatted}}` | Meeting end time in local timezone (or empty if no transcript) |
| `{{.Meeting.DurationFormatted}}` | Duration (e.g. `1h 14m` or empty if no transcript) |
| `{{.Meeting.ID}}` | Unique meeting identifier |
| `{{.Meeting.Notes}}` | Meeting notes content (or empty string) |
| `{{.Meeting.Transcript}}` | Transcript built from `transcript_segment_template.md`, each segment rendered individually |

**Conditionals** hide sections when a variable is empty:

```markdown
{{if .Meeting.Notes}}{{.Meeting.Notes}}{{else}}_No notes_{{end}}
```

Both templates are read from disk every run. The binary searches from the file's directory upward to the output root, so you can use different templates per folder.

### Customizing the Segment Template

Edit `transcript_segment_template.md` to control how each speaker turn is formatted. Per-segment variables:

| Variable | Description |
| - | - |
| `{{.TranscriptSegment.Speaker}}` | Speaker label (e.g. `Speaker A`, `Speaker B`, or previous speaker on continuation) |
| `{{.TranscriptSegment.Text}}` | Segment text without speaker prefix |
| `{{.TranscriptSegment.Source}}` | Source label for the segment (e.g. `diarization` from the Granola API) |
| `{{.TranscriptSegment.IsFinal}}` | Whether the segment is a final transcription (`true`/`false`) |
| `{{.TranscriptSegment.Index}}` | 0-based index of the segment |

The segment template is rendered separately for each segment, then the results are joined with blank lines. The first segment without the speaker prefix inherits the previous speaker's label.

### Folder Organization

Meetings placed in Granola "lists" (folders) are organized into subdirectories. If a meeting belongs to multiple lists, the file goes in the first list's directory (sorted alphabetically by list title), and hardlinks (falling back to symlinks, then copies) are created in the other list directories.

Transcript timestamps are removed when every segment has the same timestamp value (the common case).

## Requirements

### Supported OSes

| OS | Status | Notes |
| - | - | - |
| **Windows** | Full | CDP token extraction, DPAPI decryption, Credential Manager, refresh token extraction |
| **macOS** | Full | CDP token extraction, Keychain decryption, refresh token extraction |
| **Linux** | Full | Export only (no Granola desktop needed) via `-refresh-token-file` |

### Dependencies

- **Go 1.24+** (to build from source)
- **PowerShell** (Windows DPAPI operations, always present on Windows)
- **Granola desktop app** (for CDP-based token extraction or refresh token extraction; optional if you have a `refresh_token.json` from another machine)

Build from source — see [Building](#building).

## Architecture

Granola-Exporter uses a **fallback chain** of extraction methods, each tried in order until one succeeds. File-based credentials are tried first (faster, no app launch), falling through to CDP:

```text
File-based credentials + Private API
  → supabase.json.enc (DPAPI on Windows, Keychain on macOS)
  → supabase.json.enc.bak (encrypted backup)
  → Windows Credential Manager (cross-keychain format)
    → CDP token + Private API (launches Granola with --remote-debugging-port)
```

### Method Details

#### 1. File-based Credentials + Private API (`internal/api/` + `internal/encryptedjson/`)

Reads WorkOS session tokens from Granola's on-disk credential files. Supports these sources:

| Source | Format | Platform |
| - | - | - |
| `supabase.json.enc` | AES-256-GCM (12-byte nonce + ciphertext + 16-byte tag), key from `storage.dek` | All platforms |
| `supabase.json.enc.bak` | Encrypted backup (same scheme) | Same as above |
| Windows CredMan | `cross-keychain` format | Windows |

Once credentials are obtained, WorkOS token refresh is attempted if the access token is expired. The Private API provides ProseMirror-formatted notes (with fallbacks: ProseMirror `notes` → `last_viewed_panel.content` HTML-to-markdown), dedicated transcript endpoint, and document-to-list (folder) mappings. If the list endpoint returns empty notes, a per-document detail fetch (`POST /v1/get-document`) is tried as a second pass.

#### 2. CDP Token + Private API (`internal/cdp/` + `internal/api/`)

Launches Granola with `--remote-debugging-port=9222`, connects via Chrome DevTools Protocol, and calls `window.electron.ipcInvoke('get-session')` to extract the live JWT. Uses this token with Granola's private REST API at `api.granola.ai`. This is the fallback path when file-based credentials are unavailable.

### Encrypted File Format

The DEK (Data Encryption Key) chain on Windows:

```text
Local State
  os_crypt.encrypted_key (base64)
    → strip 5-byte "DPAPI" prefix
    → DPAPI Unprotect (scope=CurrentUser)
    → 32-byte AES-256 key
        ↓
storage.dek (75 bytes)
  "v10" | nonce(12) | ciphertext(44) | tag(16)
    → AES-256-GCM decrypt with AES key
    → base64-encoded DEK (44 chars)
        ↓
DEK key (32 bytes hex, machine-specific)
```

The DEK key decrypts all `.enc` files in `%APPDATA%\Granola\`:

| Encrypted | Decrypted | Size | Content |
| - | - | - | - |
| `supabase.json.enc` | WorkOS tokens + session + user info | ~2.6 KB | `access_token`, `refresh_token`, `session_id`, `user_info` (email, name) |
| `stored-accounts.json.enc` | Cross-app auth accounts | ~3 KB | Account tokens for multiple sign-in methods |
| `user-preferences.json.enc` | App preferences | ~5 KB | Theme, folders, onboarding state, paywall status |
| `cache-v6.json.enc` | Full app cache | ~1 MB | Panel templates, document lists, transcripts, calendars, recipes, workspace data, feature flags |

Each `.enc` file format: `nonce(12)` | `ciphertext` | `tag(16)` — AES-256-GCM with the DEK key, no AAD.

To decrypt manually (PowerShell):

```powershell
$ls = Get-Content "$env:APPDATA\Granola\Local State" -Raw | ConvertFrom-Json
$encKey = [Convert]::FromBase64String($ls.os_crypt.encrypted_key)[5..]  # strip "DPAPI"
$aesKey = [System.Security.Cryptography.ProtectedData]::Unprotect(
    $encKey, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser)

$dekBytes = [System.IO.File]::ReadAllBytes("$env:APPDATA\Granola\storage.dek")
$dek = [byte[]]::new(44)
$gcm = [System.Security.Cryptography.AesGcm]::new($aesKey, 16)
$gcm.Decrypt($dekBytes[3..14], $dekBytes[15..58], $dekBytes[59..74], $dek)
$gcm.Dispose()
$dekKey = [Convert]::FromBase64String([System.Text.Encoding]::UTF8.GetString($dek))

$enc = [System.IO.File]::ReadAllBytes("$env:APPDATA\Granola\supabase.json.enc")
$plain = [byte[]]::new($enc.Length - 28)
$gcm = [System.Security.Cryptography.AesGcm]::new($dekKey, 16)
$gcm.Decrypt($enc[0..11], $enc[12..($enc.Length-17)], $enc[($enc.Length-16)..], $plain)
$gcm.Dispose()
[System.Text.Encoding]::UTF8.GetString($plain)
```

### Token Refresh

WorkOS refresh tokens are used to obtain fresh access tokens:

```text
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

Documents already in the manifest are skipped by default (incremental mode). Use `-export-all` to ignore the manifest and re-export everything. The API path sorts documents by `CreatedAt` descending and stops at the first already-exported document.

Earlier projects exploring the undocumented Granola API — [graincrawl](https://github.com/graincrawl), [theantichris/granola](https://github.com/theantichris/granola), and [magarcia/granola-cli](https://github.com/magarcia/granola-cli) — provided inspiration and useful reference for this work.

## Suggested Improvements

### Decryption

- **macOS Keychain verification**: The AES-GCM decryption path for macOS (`internal/encryptedjson/`) follows the standard Electron safeStorage format but has not been tested on actual macOS hardware.

### Features

- **Notifications**: Slack/email/webhook on completion or failure.

### Code Quality

- **Rate limiting**: The Private API client has basic retry but no adaptive rate limiting.

## Glossary

| Term | Description |
| - | - |
| **CDP** (Chrome DevTools Protocol) | Protocol used to inspect and control Chromium-based browsers. Granola-Exporter launches Granola with `--remote-debugging-port` and uses CDP to extract the session JWT from the renderer process. |
| **DPAPI** (Data Protection API) | Windows encryption service that protects data at rest using the logged-in user's credentials. Used to decrypt Granola's `supabase.json.enc` credential file. |
| **ProseMirror** | Rich text editor framework. Granola stores notes as ProseMirror JSON documents; the exporter converts them to markdown. |
| **WorkOS** | Identity platform that provides Granola's authentication (login, token issuance, refresh). The exporter uses WorkOS refresh tokens to obtain fresh access tokens. |
| **JWT** (JSON Web Token) | Auth token used to authenticate API requests. Granola-Exporter extracts the JWT from the desktop app's session or from credential files. |
| **REST API** | Granola's private HTTP API at `api.granola.ai` — endpoints for documents, transcripts, and folder metadata. The primary data source for exports. |

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
ExecStart=/usr/local/bin/granola-backup -output /var/backups/granola
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

```console
sudo systemctl daemon-reload
sudo systemctl enable granola-backup.timer
sudo systemctl start granola-backup.timer
```

Check logs with `journalctl -u granola-backup.service`.

## Docker

Build and run the export in a lightweight Linux container (25 MB). Only the `-refresh-token-file` path works — token extraction (`-extract-token-only`) requires the Granola desktop app and won't run inside a container.

```console
# Build
docker compose build

# Run (mounts ./refresh_token.json and ./output/)
docker compose up

# Or manually:
docker run --rm \
  -v ./refresh_token.json:/token/refresh_token.json:ro \
  -v ./output:/output \
  granola-backup
```

## License

MIT
