package encryptedjson

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type localState struct {
	OSCrypt struct {
		EncryptedKey string `json:"encrypted_key"`
	} `json:"os_crypt"`
}

func DecryptSupabaseJSON(path string) ([]byte, error) {
	switch runtime.GOOS {
	case "windows":
		return decryptWindows(path)
	case "darwin":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return decryptMacOSKeychain(data)
	default:
		return nil, fmt.Errorf("unsupported platform for encrypted json: %s", runtime.GOOS)
	}
}

func granolaDir(encPath string) string {
	dir := filepath.Dir(encPath)
	if _, err := os.Stat(filepath.Join(dir, "Local State")); err == nil {
		return dir
	}
	appData := os.Getenv("APPDATA")
	if appData != "" {
		return filepath.Join(appData, "Granola")
	}
	return dir
}

func decryptWindows(encPath string) ([]byte, error) {
	gDir := granolaDir(encPath)

	aesKey, err := decryptLocalStateKey(filepath.Join(gDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("local state key: %w", err)
	}

	dek, err := decryptDEK(filepath.Join(gDir, "storage.dek"), aesKey)
	if err != nil {
		return nil, fmt.Errorf("storage.dek: %w", err)
	}

	return decryptAESGCMFile(encPath, dek)
}

func decryptLocalStateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ls localState
	if err := json.Unmarshal(b, &ls); err != nil {
		return nil, fmt.Errorf("parse local state: %w", err)
	}
	if ls.OSCrypt.EncryptedKey == "" {
		return nil, fmt.Errorf("no os_crypt.encrypted_key in local state")
	}

	encKey, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted_key: %w", err)
	}
	if len(encKey) < 5 || string(encKey[:5]) != "DPAPI" {
		return nil, fmt.Errorf("unexpected encrypted_key format")
	}

	dpapiBlob := base64.StdEncoding.EncodeToString(encKey[5:])

	script := `Add-Type @"
using System;
using System.Runtime.InteropServices;
public class DPAPI {
    [DllImport("crypt32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
    private static extern bool CryptUnprotectData(
        ref DATA_BLOB pDataIn, string ppszDataDescr,
        ref DATA_BLOB pOptionalEntropy, IntPtr pvReserved,
        IntPtr pPromptStruct, int dwFlags, ref DATA_BLOB pDataOut);
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
    private struct DATA_BLOB { public int cbData; public IntPtr pbData; }
    public static byte[] Unprotect(byte[] data) {
        DATA_BLOB inBlob = new DATA_BLOB();
        DATA_BLOB outBlob = new DATA_BLOB();
        DATA_BLOB entropy = new DATA_BLOB();
        inBlob.cbData = data.Length;
        inBlob.pbData = Marshal.AllocHGlobal(data.Length);
        Marshal.Copy(data, 0, inBlob.pbData, data.Length);
        try {
            if (!CryptUnprotectData(ref inBlob, null, ref entropy, IntPtr.Zero, IntPtr.Zero, 0, ref outBlob))
                throw new Exception("" + Marshal.GetLastWin32Error());
            byte[] result = new byte[outBlob.cbData];
            Marshal.Copy(outBlob.pbData, result, 0, outBlob.cbData);
            return result;
        } finally {
            if (inBlob.pbData != IntPtr.Zero) Marshal.FreeHGlobal(inBlob.pbData);
            if (outBlob.pbData != IntPtr.Zero) Marshal.FreeHGlobal(outBlob.pbData);
        }
    }
}
"@
$bytes = [Convert]::FromBase64String('` + dpapiBlob + `')
$plain = [DPAPI]::Unprotect($bytes)
[System.Console]::Write([System.Convert]::ToBase64String($plain))
`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("dpapi: %v stderr=%s", err, stderr.String())
	}
	outStr := strings.TrimSpace(stdout.String())
	if outStr == "" {
		return nil, fmt.Errorf("dpapi: empty output")
	}
	return base64.StdEncoding.DecodeString(outStr)
}

func decryptDEK(path string, aesKey []byte) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 31 || string(data[:3]) != "v10" {
		return nil, fmt.Errorf("invalid storage.dek format")
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := data[3:15]
	ctWithTag := data[15:]

	plain, err := gcm.Open(nil, nonce, ctWithTag, nil)
	if err != nil {
		return nil, fmt.Errorf("aes-gcm: %w", err)
	}

	return base64.StdEncoding.DecodeString(string(plain))
}

func decryptAESGCMFile(path string, key []byte) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 29 {
		return nil, fmt.Errorf("encrypted file too short: %d bytes", len(data))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := data[:12]
	ctWithTag := data[12:]

	return gcm.Open(nil, nonce, ctWithTag, nil)
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
