package encryptedjson

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func DecryptSupabaseJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty encrypted file")
	}

	switch runtime.GOOS {
	case "windows":
		return decryptWindowsDPAPI(path, data)
	case "darwin":
		return decryptMacOSKeychain(data)
	default:
		return nil, fmt.Errorf("unsupported platform for encrypted json: %s", runtime.GOOS)
	}
}

func decryptWindowsDPAPI(path string, data []byte) ([]byte, error) {
	b64 := base64.StdEncoding.EncodeToString(data)

	script := fmt.Sprintf(`$bytes = [Convert]::FromBase64String('%s')
try {
    $plain = [System.Security.Cryptography.ProtectedData]::Unprotect($bytes, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser)
    [System.Console]::Write([System.Convert]::ToBase64String($plain))
} catch {
    [System.Console]::Error.WriteLine("DPAPI error: " + $_.Exception.Message)
    exit 1
}`, b64)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		cmd2 := exec.Command("pwsh", "-NoProfile", "-Command", script)
		var stdout2, stderr2 strings.Builder
		cmd2.Stdout = &stdout2
		cmd2.Stderr = &stderr2

		if err2 := cmd2.Run(); err2 != nil {
			return nil, fmt.Errorf("DPAPI decrypt (powershell: %v; pwsh: %v)\npowershell stderr: %s\npwsh stderr: %s",
				err, err2, stderr.String(), stderr2.String())
		}
		result := strings.TrimSpace(stdout2.String())
		if result == "" {
			return nil, fmt.Errorf("DPAPI decrypt: empty output (pwsh)")
		}
		return base64.StdEncoding.DecodeString(result)
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return nil, fmt.Errorf("DPAPI decrypt: empty output")
	}
	return base64.StdEncoding.DecodeString(result)
}

func decryptMacOSKeychain(data []byte) ([]byte, error) {
	if len(data) < 31 {
		return nil, fmt.Errorf("encrypted file too short: %d bytes", len(data))
	}

	if string(data[:3]) != "v10" {
		return nil, fmt.Errorf("unknown encrypted file header: %q", string(data[:3]))
	}

	nonce := data[3:15]
	tag := data[len(data)-16:]
	ciphertext := data[15 : len(data)-16]

	keyB64, err := exec.Command("security", "find-generic-password",
		"-s", "Granola Safe Storage",
		"-a", "Granola Key",
		"-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain: %w", err)
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyB64)))
	if err != nil {
		return nil, fmt.Errorf("decode keychain key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aead, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, nonce, append(ciphertext, tag...), nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM decrypt: %w", err)
	}

	return plaintext, nil
}
