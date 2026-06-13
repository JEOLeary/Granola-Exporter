package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/JEOLeary/granola-backup/internal/output"
)

var mcpServerURL = "https://mcp.granola.ai/mcp"

const (
	mcpAuthServer    = "https://mcp-auth.granola.ai"
	mcpClientID      = "client_01KTN4Q7CZZJ7JGZ7WXBQ0813C"
	mcpScope         = "mcp"
	defaultTokenFile = "granola_token.json"
)

type MCPClient struct {
	httpClient *http.Client
	tokenFile  string
	token      *MCPToken
}

type MCPToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"-"`
}

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type NoteItem struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	NotesMarkdown string `json:"notes_markdown"`
	Transcript    string `json:"transcript,omitempty"`
}

type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

func NewMCPClient(tokenFile string) *MCPClient {
	if tokenFile == "" {
		tokenFile = defaultTokenFile
	}
	return &MCPClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		tokenFile:  tokenFile,
	}
}

func (c *MCPClient) LoadToken() error {
	data, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return err
	}
	var tok MCPToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return err
	}
	c.token = &tok
	return nil
}

func (c *MCPClient) SaveToken() error {
	data, err := json.MarshalIndent(c.token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.tokenFile, data, 0600)
}

func (c *MCPClient) DeviceAuth() error {
	fmt.Println("\n=== Granola MCP Authentication (Device Flow) ===")
	fmt.Println()

	resp, err := c.httpClient.PostForm(
		mcpAuthServer+"/oauth2/device_authorization",
		url.Values{
			"client_id": {mcpClientID},
			"scope":     {mcpScope},
		},
	)
	if err != nil {
		return fmt.Errorf("device auth request: %w", err)
	}
	defer resp.Body.Close()

	var deviceResp DeviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return fmt.Errorf("device auth decode: %w", err)
	}

	authURL := deviceResp.VerificationURIComplete
	if authURL == "" {
		authURL = deviceResp.VerificationURI
	}

	fmt.Println("Open this URL in your browser:")
	fmt.Println()
	fmt.Println("  " + authURL)
	if deviceResp.VerificationURIComplete == "" {
		fmt.Println()
		fmt.Println("Enter this code:")
		fmt.Println()
		fmt.Println("  " + deviceResp.UserCode)
	}
	fmt.Println()
	fmt.Printf("Code expires in %d seconds. Polling every %ds...\n",
		deviceResp.ExpiresIn, deviceResp.Interval)
	fmt.Println()

	interval := deviceResp.Interval
	start := time.Now()
	deadline := start.Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		tokenResp, err := c.httpClient.PostForm(
			mcpAuthServer+"/oauth2/token",
			url.Values{
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
				"device_code": {deviceResp.DeviceCode},
				"client_id":   {mcpClientID},
			},
		)
		if err != nil {
			return fmt.Errorf("token poll: %w", err)
		}

		if tokenResp.StatusCode == 200 {
			var tok MCPToken
			if err := json.NewDecoder(tokenResp.Body).Decode(&tok); err != nil {
				tokenResp.Body.Close()
				return fmt.Errorf("token decode: %w", err)
			}
			tokenResp.Body.Close()
			c.token = &tok
			c.token.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
			if err := c.SaveToken(); err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			fmt.Println("Authentication successful!")
			return nil
		}

		var errBody struct {
			Error string `json:"error"`
		}
		json.NewDecoder(tokenResp.Body).Decode(&errBody)
		tokenResp.Body.Close()

		switch errBody.Error {
		case "authorization_pending":
			fmt.Print(".")
		case "slow_down":
			interval += 5
			fmt.Print("_")
		case "expired_token":
			tokenResp.Body.Close()
			return fmt.Errorf("device code expired, please re-run")
		default:
			tokenResp.Body.Close()
			return fmt.Errorf("auth error: %s", errBody.Error)
		}
	}

	return fmt.Errorf("timed out waiting for authorization")
}

func (c *MCPClient) EnsureToken() error {
	if c.token != nil && time.Now().Before(c.token.ExpiresAt) {
		return nil
	}

	if c.token != nil && c.token.RefreshToken != "" {
		resp, err := c.httpClient.PostForm(
			mcpAuthServer+"/oauth2/token",
			url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {c.token.RefreshToken},
				"client_id":     {mcpClientID},
			},
		)
		if err == nil && resp.StatusCode == 200 {
			var tok MCPToken
			if err := json.NewDecoder(resp.Body).Decode(&tok); err == nil {
				resp.Body.Close()
				if tok.RefreshToken == "" {
					tok.RefreshToken = c.token.RefreshToken
				}
				c.token = &tok
				c.token.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
				c.SaveToken()
				return nil
			}
			resp.Body.Close()
		}
	}

	return c.DeviceAuth()
}

func (c *MCPClient) callMCP(method string, params interface{}, result interface{}) error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", mcpServerURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.token.AccessToken)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("JSON-RPC decode: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("MCP error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if result != nil && rpcResp.Result != nil {
		return json.Unmarshal(rpcResp.Result, result)
	}

	return nil
}

func (c *MCPClient) ListTools() ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.callMCP("tools/list", struct{}{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *MCPClient) ListNotes() ([]NoteItem, error) {
	var result struct {
		Notes []NoteItem `json:"notes"`
	}
	if err := c.callMCP("notes/list", struct{}{}, &result); err != nil {
		return nil, err
	}
	return result.Notes, nil
}

func (c *MCPClient) GetNote(id string) (*NoteItem, error) {
	var result struct {
		Note NoteItem `json:"note"`
	}
	if err := c.callMCP("notes/get", map[string]string{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result.Note, nil
}

func (c *MCPClient) GetTranscript(id string) (string, error) {
	var result struct {
		Transcript string `json:"transcript"`
	}
	if err := c.callMCP("notes/transcript/get", map[string]string{"id": id}, &result); err != nil {
		return "", err
	}
	return result.Transcript, nil
}

func (c *MCPClient) ExportAll(outputDir string, overwrite bool) (int, error) {
	if err := c.EnsureToken(); err != nil {
		return 0, fmt.Errorf("auth: %w", err)
	}

	notes, err := c.ListNotes()
	if err != nil {
		return 0, fmt.Errorf("list notes: %w", err)
	}

	manifest := output.LoadManifest(outputDir)

	var pending []NoteItem
	for _, n := range notes {
		if _, exists := manifest.Contains(n.ID); exists {
			if !overwrite {
				continue
			}
		}
		pending = append(pending, n)
	}

	if len(pending) == 0 {
		return 0, nil
	}

	var mu sync.Mutex
	exported := 0
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for _, n := range pending {
		wg.Add(1)
		n := n
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fullNote, err := c.GetNote(n.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s (get): %v\n", n.ID, err)
				return
			}

			transcript := ""
			if fullNote.Transcript == "" {
				if t, err := c.GetTranscript(n.ID); err == nil {
					transcript = t
				}
			} else {
				transcript = fullNote.Transcript
			}

			createdAt, _ := time.Parse(time.RFC3339, fullNote.CreatedAt)

			m := output.Meeting{
				ID:         fullNote.ID,
				Title:      fullNote.Title,
				DateTime:   createdAt,
				Notes:      fullNote.NotesMarkdown,
				Transcript: transcript,
			}

			relPath, err := output.WriteMeeting(outputDir, m, overwrite, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s (write): %v\n", n.ID, err)
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

func (c *MCPClient) HasToken() bool {
	_, err := os.Stat(c.tokenFile)
	return err == nil
}
