package sqlcipher

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JEOLeary/granola-backup/internal/output"
)

//go:embed extract_db.js
var extractDBScript string

type SQLCipherResult struct {
	Success   bool          `json:"success"`
	Tables    []string      `json:"tables"`
	Documents []DocumentRow `json:"documents"`
	DBPath    string        `json:"dbPath"`
	Error     string        `json:"error,omitempty"`
}

type DocumentRow struct {
	Table   string          `json:"table"`
	Columns []string        `json:"columns"`
	Row     json.RawMessage `json:"row,omitempty"`
	Error   string          `json:"error,omitempty"`
}

const node24Version = "v24.16.0"
const node24URL = "https://nodejs.org/dist/" + node24Version + "/node-" + node24Version + "-win-x64.zip"

type SQLCipherExtractor struct {
	granolaDir string
	cacheDir   string
	nodePath   string
}

func NewSQLCipherExtractor(granolaPath, cacheDir, nodePath string) *SQLCipherExtractor {
	if granolaPath == "" {
		granolaPath = `C:\Users\JEOLeary\AppData\Local\Programs\granola\Granola.exe`
	}
	granolaDir := filepath.Dir(granolaPath)
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".granola-backup")
	}
	return &SQLCipherExtractor{
		granolaDir: granolaDir,
		cacheDir:   cacheDir,
		nodePath:   nodePath,
	}
}

func (s *SQLCipherExtractor) findNode() (string, error) {
	if s.nodePath != "" {
		if _, err := os.Stat(s.nodePath); err == nil {
			return s.nodePath, nil
		}
		return "", fmt.Errorf("specified node not found: %s", s.nodePath)
	}

	if p, err := exec.LookPath("node"); err == nil {
		out, _ := exec.Command(p, "-e", "console.log(process.versions.node)").Output()
		ver := strings.TrimSpace(string(out))
		if isNode24(ver) {
			return p, nil
		}
	}

	cached := filepath.Join(s.cacheDir, "node24", "node.exe")
	if _, err := os.Stat(cached); err == nil {
		return cached, nil
	}

	electronNode := filepath.Join(s.granolaDir, "node.exe")
	if _, err := os.Stat(electronNode); err == nil {
		return electronNode, nil
	}

	return "", fmt.Errorf("Node.js 24+ not found")
}

func isNode24(ver string) bool {
	if len(ver) < 2 {
		return false
	}
	major := 0
	fmt.Sscanf(ver, "%d", &major)
	return major >= 24
}

func (s *SQLCipherExtractor) downloadNode() (string, error) {
	nodeDir := filepath.Join(s.cacheDir, "node24")
	zipPath := filepath.Join(s.cacheDir, "node24.zip")

	if _, err := os.Stat(filepath.Join(nodeDir, "node.exe")); err == nil {
		return filepath.Join(nodeDir, "node.exe"), nil
	}

	os.MkdirAll(s.cacheDir, 0755)

	fmt.Println("Downloading Node.js " + node24Version + "...")
	fmt.Println("  " + node24URL)

	resp, err := http.Get(node24URL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download: HTTP %d: %s", resp.StatusCode, string(body))
	}

	out, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("create zip: %w", err)
	}

	written, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("download copy: %w", err)
	}
	fmt.Printf("  Downloaded %d bytes\n", written)

	os.RemoveAll(nodeDir)
	os.MkdirAll(nodeDir, 0755)

	if err := extractZip(zipPath, nodeDir); err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("extract: %w", err)
	}
	os.Remove(zipPath)

	nestedDir := filepath.Join(nodeDir, "node-"+node24Version+"-win-x64")
	if _, err := os.Stat(nestedDir); err == nil {
		entries, _ := os.ReadDir(nestedDir)
		for _, e := range entries {
			old := filepath.Join(nestedDir, e.Name())
			neu := filepath.Join(nodeDir, e.Name())
			if err := os.Rename(old, neu); err != nil {
				return "", fmt.Errorf("move %s: %w", e.Name(), err)
			}
		}
		os.Remove(nestedDir)
	}

	exe := filepath.Join(nodeDir, "node.exe")
	if _, err := os.Stat(exe); err != nil {
		return "", fmt.Errorf("node.exe not found after extraction")
	}

	fmt.Println("  Extracted to " + nodeDir)
	return exe, nil
}

func extractZip(zipPath, dest string) error {
	cmd := exec.Command("pwsh", "-NoProfile", "-Command",
		"Expand-Archive -Path '"+zipPath+"' -DestinationPath '"+dest+"' -Force")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *SQLCipherExtractor) decryptKey() (string, string, error) {
	appData := os.Getenv("APPDATA")
	granolaData := filepath.Join(appData, "Granola")

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Security.Cryptography

$localState = Get-Content '%s' -Raw | ConvertFrom-Json
$encryptedKey = [Convert]::FromBase64String($localState.os_crypt.encrypted_key)
$encryptedKey = $encryptedKey[5..($encryptedKey.Length-1)]
$dek = [System.Security.Cryptography.ProtectedData]::Unprotect($encryptedKey, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser)
$aesKey = [System.BitConverter]::ToString($dek).Replace('-','')

$dekPath = '%s'
$dekBytes = [System.IO.File]::ReadAllBytes($dekPath)
$version = [System.Text.Encoding]::ASCII.GetString($dekBytes[0..2])
if ($version -ne 'v10') { throw 'Unknown DEK version: ' + $version }
$iv = $dekBytes[3..14]
$ciphertext = $dekBytes[15..($dekBytes.Length-17)]
$tag = $dekBytes[($dekBytes.Length-16)..($dekBytes.Length-1)]

$aes = [System.Security.Cryptography.AesGcm]::new([Convert]::FromHexString($aesKey), 16)
$plaintext = [byte[]]::new($ciphertext.Length)
$aes.Decrypt($iv, $ciphertext, $tag, $plaintext)
$base64Key = [System.Text.Encoding]::UTF8.GetString($plaintext)
$hexKey = [System.BitConverter]::ToString([Convert]::FromBase64String($base64Key)).Replace('-','')
Write-Host $hexKey
`, filepath.Join(granolaData, "Local State"), filepath.Join(granolaData, "storage.dek"))

	cmd := exec.Command("pwsh", "-NoProfile", "-Command", script)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("key decrypt: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	outStr := strings.TrimSpace(stdout.String())
	if outStr == "" {
		errStr := strings.TrimSpace(stderr.String())
		return "", "", fmt.Errorf("key decrypt: empty output\nstderr: %s", errStr)
	}
	dbPath := filepath.Join(granolaData, "granola.db")

	if _, err := os.Stat(dbPath); err != nil {
		return "", "", fmt.Errorf("granola.db not found: %w", err)
	}

	return dbPath, outStr, nil
}

func (s *SQLCipherExtractor) extractViaNode(nodeExe, dbPath, hexKey string) (*SQLCipherResult, error) {
	modulePath := filepath.Join(s.granolaDir, "resources", "app.asar.unpacked", "node_modules", "better-sqlite3-multiple-ciphers")

	scriptFile := filepath.Join(s.cacheDir, "extract_db.js")
	os.WriteFile(scriptFile, []byte(extractDBScript), 0644)

	cmd := exec.Command(nodeExe, scriptFile, dbPath, hexKey, modulePath)
	cmd.Dir = s.cacheDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("node exec: %w\nstderr: %s", err, stderr.String())
	}

	if stderr.Len() > 0 {
		var errResult SQLCipherResult
		if json.Unmarshal(stderr.Bytes(), &errResult) == nil && errResult.Error != "" {
			return nil, fmt.Errorf("extract error: %s", errResult.Error)
		}
	}

	var result SQLCipherResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse output: %w\nraw: %s", err, stdout.String())
	}

	if !result.Success {
		return nil, fmt.Errorf("extract failed: %s", result.Error)
	}

	return &result, nil
}

func (s *SQLCipherExtractor) ExportAll(outputDir string, overwrite bool) (int, error) {
	nodeExe, err := s.findNode()
	if err != nil {
		fmt.Println("Node.js 24+ not found, downloading...")
		nodeExe, err = s.downloadNode()
		if err != nil {
			return 0, fmt.Errorf("node download: %w", err)
		}
		fmt.Println("  Using: " + nodeExe)
	}

	fmt.Println("Decrypting key and opening database...")
	dbPath, hexKey, err := s.decryptKey()
	if err != nil {
		return 0, fmt.Errorf("key: %w", err)
	}

	result, err := s.extractViaNode(nodeExe, dbPath, hexKey)
	if err != nil {
		return 0, fmt.Errorf("extract: %w", err)
	}

	fmt.Printf("  Tables: %v\n", result.Tables)

	type DocEntry struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		CreatedAt string `json:"created_at"`
		NotesMD   string `json:"notes_markdown"`
	}

	var meetings []output.Meeting
	for _, doc := range result.Documents {
		var entry DocEntry
		if err := json.Unmarshal(doc.Row, &entry); err != nil {
			continue
		}
		if entry.Title == "" {
			continue
		}

		createdAt, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		meetings = append(meetings, output.Meeting{
			ID:       entry.ID,
			Title:    entry.Title,
			DateTime: createdAt,
			Notes:    entry.NotesMD,
		})
	}

	manifest := output.LoadManifest(outputDir)

	var pending []output.Meeting
	for _, m := range meetings {
		if m.ID != "" {
			if _, exists := manifest.Contains(m.ID); exists && !overwrite {
				continue
			}
		}
		pending = append(pending, m)
	}

	if len(pending) == 0 {
		return 0, nil
	}

	var mu sync.Mutex
	exported := 0
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for _, m := range pending {
		wg.Add(1)
		m := m
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			relPath, err := output.WriteMeeting(outputDir, m, overwrite, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s (write): %v\n", m.ID, err)
				return
			}
			if relPath == "" {
				return
			}

			mu.Lock()
			manifest.Add(m.ID, relPath, m.DateTime)
			manifest.Save(outputDir)
			exported++
			mu.Unlock()

			fmt.Println("  " + relPath)
		}()
	}

	wg.Wait()
	return exported, nil
}
