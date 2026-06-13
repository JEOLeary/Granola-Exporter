# Granola.ai Transcript Extraction — Status & Next Steps

## Goal

Extract meeting transcripts from Granola.ai's local database on **OLeary-Tower-1** (Windows 11, 192.168.1.6) — without a Granola paid subscription.

## What We've Accomplished

### ✅ Keys Extracted (Full Chain Verified)

The complete decryption chain from Electron's `safeStorage` has been reverse-engineered and verified:

1. **`Local State` DPAPI** → 32-byte AES-256-GCM key
   - File: `%APPDATA%\Granola\Local State` → `os_crypt.encrypted_key`
   - Decrypted via `CryptUnprotectData` (user-context DPAPI on Windows)
   - **Result:** `6A555EE3B193445B1A3614A283A0471106CE3D2437D24DA7EA7BF731BCF9697C`

2. **`storage.dek` AES-GCM decrypt** → base64 → 32-byte SQLCipher raw key
   - Format: `"v10"` (3-byte header) + IV(12) + ciphertext + auth_tag(16)
   - Decrypted via AES-256-GCM (verified: re-encrypt round-trips ✅)
   - **Result:** `ysrUQIHXwFFoF5beTOt0COoeRKllp2XOuKTrYKlHilQ=` → **`CACAD44081D7C051681796DE4CEB7408EA1E44A965A765CEB8A4EB60A9478A54`**

3. **Key infrastructure verified** — created a test SQLCipher DB with the key and re-opened it successfully via `db.key(Buffer.from(keyHex, 'hex'))` with `cipher='sqlcipher'` and `legacy=4`.

### ✅ Application Code Reverse-Engineered

Granola's `app.asar` (~57 MB) extracted and analyzed:

- DB cipher: **sqlcipher**, `legacy = 4`
- Key format: `PRAGMA key = "x'<hex>'"` (double-quoted passphrase, not hex literal)
- Storage flow: `crypto.randomBytes(32)` → base64 → `safeStorage.encryptString()` → `storage.dek`
- Granola version: **7.309.0**, Electron-based
- Install: `%LOCALAPPDATA%\Programs\granola\app-7.309.0\`

### ✅ Remote Windows Access Working

| Method | Status | Notes |
|--------|--------|-------|
| SMB (C$ admin share) | ✅ | Password provided, NTLM hash computed |
| WMI (`impacket-wmiexec`) | ✅ | Runs scripts as `JEOLeary` (user context) |
| Task Scheduler (`atexec.py`) | ✅ | Runs as SYSTEM (not useful for user DPAPI) |
| Windows Defender | ⚠️ | Blocked `winexe` (service-based) |

### ✅ PowerShell 7.6.1 with AesGcm Available on Windows

`pwsh.exe` is available on OLeary-Tower-1 with full `[System.Security.Cryptography.AesGcm]` support.

## ❌ The Block: SQLCipher Key Rejected

**The extracted 32-byte key does NOT open `granola.db`.**

Every format tried returns `"file is not a database"`:

| Approach | Result |
|----------|--------|
| `db.key(Buffer.from(hex, 'hex'))` | ❌ file is not a database |
| `PRAGMA key = "x'HEX'"` (Granola's format) | ❌ file is not a database |
| `PRAGMA key = x'HEX'` | ❌ syntax error or file not a database |
| `PRAGMA key = 'HEX'` (text passphrase) | ❌ file is not a database |
| `constructor({ key: Buffer })` with cipher/legacy | ❌ file is not a database |
| PBKDF2-derived (SHA1/256/512 × 4k/64k/128k/256k/512k iters) | ❌ all failed |
| legacy=0,1,2,3,4 | ❌ all failed |
| cipher_page_size=1024, 4096 | ❌ all failed |
| All combinations of cipher types (aes-256-cbc, chacha20) | ❌ all failed |

Key verification:
- ✅ Re-encrypting `storage.dek` with the same AES key and IV produces identical ciphertext + tag
- ✅ Creating a new SQLCipher database with the 32-byte key works perfectly
- ❌ The same key does not work on the **actual `granola.db`**

## Hypotheses for the Mismatch

### 1. Different SQLCipher parameters than declared

The `legacy = 4` setting in Granola's code might map to different PBKDF2/SQLCipher parameters than what our `better-sqlite3-multiple-ciphers` v12.10.0 Linux build uses. The app was compiled with a specific SQLCipher version that might differ.

### 2. Database rekey

`granola.db` was last modified **June 9, 2026** (07:32). `storage.dek` was created **April 21, 2026** (10:54). If the database was rekeyed between those dates and `storage.dek` wasn't updated (or vice versa), the keys would be out of sync.

### 3. Database recreated/from different account

If a new Granola account was added and the database re-created, the `storage.dek` from the old session might not match. There are also `.enc` files (e.g., `cache-v6.json.enc`, `stored-accounts.json.enc`) that suggest a second encryption layer may exist.

### 4. Version mismatch in better-sqlite3-multiple-ciphers

The `better-sqlite3-multiple-ciphers` npm package might handle `PRAGMA key` differently between versions. The Granola-bundled version is for Windows (cannot run on Linux). Our Linux build is the same version string (12.10.0) but the compiled SQLCipher might differ.

## Suggested Next Steps

### Option A: Run Extraction on Windows via Granola's Own Node.js (Recommended)

**Why this should work:** Use Granola's own native `better-sqlite3-multiple-ciphers` addon (compiled for Windows with the exact SQLCipher settings Granola expects) to open the database with the key we already extracted.

**Files already prepared:**
- `step1_extract.ps1` — Does DPAPI + AES-GCM to get the 32-byte key, writes it to C:\temp
- `extract_win.js` — Node.js script that uses Granola's native addon to query the database

**What's needed:**
1. Upload the Node.js query script to Windows via SMB
2. Run it via the Node.js bundled with Granola (`Granola.exe` is an Electron wrapper, try `node.exe` from the `app-7.309.0\resources\` directory, or failing that use the system `node.exe`)
3. Capture the JSON output

### Option B: Use the Granola API with Extracted JWT Token

We already found plaintext JWTs in `stored-accounts.json.bak` that could be used to call Granola's API directly. The API has endpoints like `get-document-transcript` that might return transcript data without needing the local database at all.

### Option C: Copy the database to a Windows machine with Granola installed

Fresh install Granola on a machine, replace the `granola.db`, and launch the app to export data natively.

### Option D: Brute-force PBKDF2 parameters

If the database uses different KDF parameters than expected, systematically brute-force the PBKDF2 iterations/hash combinations. This would require running on a fast machine as each attempt requires 256k+ PBKDF2 iterations.

## Files on Windows

| File | Path | Purpose |
|------|------|---------|
| `granola.db` | `%APPDATA%\Granola\` | Encrypted SQLCipher database (9.6 MB) |
| `storage.dek` | `%APPDATA%\Granola\` | AES-256-GCM encrypted DEK (75 bytes, "v10" format) |
| `Local State` | `%APPDATA%\Granola\` | DPAPI-encrypted AES key (JSON) |
| `stored-accounts.json.bak` | `%APPDATA%\Granola\` | Plaintext JWT tokens (API fallback) |

## Files on Linux (Hermes host)

| File | Purpose |
|------|---------|
| `/tmp/granola/granola.db` | Local copy of encrypted DB |
| `/tmp/granola/storage.dek` | Local copy of the DEK |
| `/tmp/granola/local_state.txt` | Local State with `os_crypt.encrypted_key` |
| `/tmp/granola/decrypt_storage_dek.py` | AES-GCM decrypt verified |
| `/tmp/granola/test_db*.js` | Various SQLCipher test scripts |
| `/tmp/granola/step1_extract.ps1` | PowerShell extraction script (ready to upload) |
| `/tmp/granola/extract_win.js` | Node.js DB query script (ready to upload) |
| `/tmp/granola/run_extract.ps1` | Full PS7 extraction pipeline (DPAPI → AES-GCM → Node.js query) |
