package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMCPCallMCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"status":"ok"}`),
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	var result map[string]string
	err := client.callMCP("test/method", map[string]string{"key": "val"}, &result)
	if err != nil {
		t.Fatalf("callMCP() error = %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestMCPCallMCPAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "bad-token"}

	err := client.callMCP("test/method", nil, nil)
	if err == nil {
		t.Fatal("callMCP() expected error for auth failure")
	}
}

func TestMCPCallMCPRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &JSONRPCError{Code: -32000, Message: "something went wrong"},
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	err := client.callMCP("test/method", nil, nil)
	if err == nil {
		t.Fatal("callMCP() expected error for RPC error response")
	}
}

func TestMCPListNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result: json.RawMessage(`{"notes":[
				{"id":"n1","title":"Meeting 1","created_at":"2026-06-01T00:00:00Z"},
				{"id":"n2","title":"Meeting 2","created_at":"2026-06-02T00:00:00Z"}
			]}`),
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	notes, err := client.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	if notes[0].ID != "n1" || notes[1].ID != "n2" {
		t.Errorf("note IDs = %q, %q", notes[0].ID, notes[1].ID)
	}
}

func TestMCPGetNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"note":{"id":"n1","title":"Test Note","notes_markdown":"# Hello","created_at":"2026-06-01T00:00:00Z"}}`),
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	note, err := client.GetNote("n1")
	if err != nil {
		t.Fatalf("GetNote() error = %v", err)
	}
	if note.ID != "n1" || note.Title != "Test Note" {
		t.Errorf("note = %+v", note)
	}
	if note.NotesMarkdown != "# Hello" {
		t.Errorf("NotesMarkdown = %q, want %q", note.NotesMarkdown, "# Hello")
	}
}

func TestMCPGetTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"transcript":"Speaker 1: Hello\n\nSpeaker 2: Hi"}`),
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	transcript, err := client.GetTranscript("n1")
	if err != nil {
		t.Fatalf("GetTranscript() error = %v", err)
	}
	if transcript != "Speaker 1: Hello\n\nSpeaker 2: Hi" {
		t.Errorf("transcript = %q", transcript)
	}
}

func TestMCPHasToken(t *testing.T) {
	t.Run("token file exists", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := dir + "/token.json"
		os.WriteFile(tokenFile, []byte(`{}`), 0644)
		client := NewMCPClient(tokenFile)
		if !client.HasToken() {
			t.Error("HasToken() = false, want true")
		}
	})

	t.Run("token file missing", func(t *testing.T) {
		client := NewMCPClient("/nonexistent/path/token.json")
		if client.HasToken() {
			t.Error("HasToken() = true, want false")
		}
	})
}

func TestMCPListTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result: json.RawMessage(`{"tools":[
				{"name":"notes/list","description":"List notes"},
				{"name":"notes/get","description":"Get a note"}
			]}`),
		})
	}))
	defer srv.Close()

	orig := mcpServerURL
	mcpServerURL = srv.URL
	defer func() { mcpServerURL = orig }()

	client := NewMCPClient("")
	client.token = &MCPToken{AccessToken: "test-token"}

	tools, err := client.ListTools()
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "notes/list" {
		t.Errorf("first tool name = %q", tools[0].Name)
	}
}
