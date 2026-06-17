package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const DefaultClientID = "client_01JZJ0XBDAT8PHJWQY09Y0VD61"

func BrowserLogin(clientID, outPath string) error {
	if clientID == "" {
		clientID = DefaultClientID
	}

	verifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generate verifier: %w", err)
	}
	challenge := codeChallenge(verifier)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	authURL := fmt.Sprintf(
		"https://api.workos.com/user_management/authorize?%s",
		url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {fmt.Sprintf("http://127.0.0.1:%d/callback", port)},
			"response_type":         {"code"},
			"code_challenge_method": {"S256"},
			"code_challenge":        {challenge},
			"scope":                 {"openid profile email"},
		}.Encode(),
	)

	fmt.Println("Opening browser for Granola login...")
	fmt.Printf("If the browser doesn't open, visit:\n  %s\n\n", authURL)

	if err := openURL(authURL); err != nil {
		fmt.Printf("Could not open browser: %v\n", err)
		fmt.Println("Open the URL above in your browser manually.")
	}

	code, err := waitForCallback(listener, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("callback: %w", err)
	}

	fmt.Println("Exchanging authorization code for tokens...")

	tokenURL := "https://api.workos.com/user_management/authenticate"
	body := map[string]string{
		"client_id":     clientID,
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  fmt.Sprintf("http://127.0.0.1:%d/callback", port),
	}
	bodyBytes, _ := json.Marshal(body)

	resp, err := http.Post(tokenURL, "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("token exchange failed: status=%d body=%s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	extractedID := ExtractClientID(result.AccessToken)
	if extractedID == "" {
		extractedID = clientID
	}

	output := struct {
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
	}{
		RefreshToken: result.RefreshToken,
		ClientID:     extractedID,
	}

	b, _ := json.MarshalIndent(output, "", "  ")
	if err := os.WriteFile(outPath, b, 0600); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("Login successful. Credentials written to: %s\n", outPath)
	return nil
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func waitForCallback(listener net.Listener, timeout time.Duration) (string, error) {
	done := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code != "" {
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte("<html><body><p>Login complete! You can close this tab.</p></body></html>"))
				done <- code
			} else {
				w.WriteHeader(400)
				w.Write([]byte("Missing code parameter"))
				errCh <- fmt.Errorf("no code in callback: %s", r.URL.String())
			}
		})
		server.Handler = mux
		server.Serve(listener)
	}()

	select {
	case code := <-done:
		server.Close()
		return code, nil
	case err := <-errCh:
		server.Close()
		return "", err
	case <-time.After(timeout):
		server.Close()
		return "", fmt.Errorf("timed out waiting for login after %v", timeout)
	}
}

func openURL(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", ""}
	case "darwin":
		cmd = "open"
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
