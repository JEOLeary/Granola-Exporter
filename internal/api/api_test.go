package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestExtractClientID(t *testing.T) {
	tests := []struct {
		name string
		jwt  string
		want string
	}{
		{
			name: "valid JWT with iss",
			jwt:  "header.eyJpc3MiOiAiaHR0cHM6Ly9hdXRoLmdyYW5vbGEuYWkvdXNlcl9tYW5hZ2VtZW50L2NsaWVudF8wMVhZWiJ9.signature",
			want: "client_01XYZ",
		},
		{
			name: "no iss in JWT",
			jwt:  "header.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
			want: "",
		},
		{
			name: "malformed JWT",
			jwt:  "not-a-jwt",
			want: "",
		},
		{
			name: "empty JWT",
			jwt:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractClientID(tt.jwt)
			if got != tt.want {
				t.Errorf("ExtractClientID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCredentialsBytes(t *testing.T) {
	t.Run("valid credentials", func(t *testing.T) {
		input := map[string]interface{}{
			"workos_tokens": mustMarshal(map[string]interface{}{
				"access_token":  "test-access-token",
				"refresh_token": "test-refresh-token",
				"client_id":     "test-client-id",
				"expires_in":    3600,
				"obtained_at":   time.Now().UnixMilli(),
			}),
		}
		b, _ := json.Marshal(input)
		creds, err := ParseCredentialsBytes(b)
		if err != nil {
			t.Fatalf("ParseCredentialsBytes() error = %v", err)
		}
		if creds.AccessToken != "test-access-token" {
			t.Errorf("AccessToken = %q, want %q", creds.AccessToken, "test-access-token")
		}
		if creds.RefreshToken != "test-refresh-token" {
			t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "test-refresh-token")
		}
	})

	t.Run("missing workos_tokens", func(t *testing.T) {
		input := map[string]interface{}{"other": "data"}
		b, _ := json.Marshal(input)
		_, err := ParseCredentialsBytes(b)
		if err == nil {
			t.Error("ParseCredentialsBytes() expected error for missing workos_tokens")
		}
	})

	t.Run("no access_token", func(t *testing.T) {
		input := map[string]interface{}{
			"workos_tokens": mustMarshal(map[string]interface{}{
				"refresh_token": "test-refresh-token",
			}),
		}
		b, _ := json.Marshal(input)
		_, err := ParseCredentialsBytes(b)
		if err == nil {
			t.Error("ParseCredentialsBytes() expected error for missing access_token")
		}
	})
}

func TestIsExpired(t *testing.T) {
	t.Run("expired token", func(t *testing.T) {
		c := Credentials{
			ObtainedAt: time.Now().Add(-2 * time.Hour).UnixMilli(),
			ExpiresIn:  3600,
		}
		if !c.IsExpired() {
			t.Error("IsExpired() = false, want true")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		c := Credentials{
			ObtainedAt: time.Now().UnixMilli(),
			ExpiresIn:  3600,
		}
		if c.IsExpired() {
			t.Error("IsExpired() = true, want false")
		}
	})

	t.Run("zero values", func(t *testing.T) {
		c := Credentials{}
		if !c.IsExpired() {
			t.Error("IsExpired() = false, want true for zero values")
		}
	})
}

func TestExtractProseMirror(t *testing.T) {
	t.Run("simple doc", func(t *testing.T) {
		input := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hello world"}]}]}`)
		got := extractProseMirror(input)
		want := "Hello world\n"
		if got != want {
			t.Errorf("extractProseMirror() = %q, want %q", got, want)
		}
	})

	t.Run("empty doc", func(t *testing.T) {
		got := extractProseMirror(json.RawMessage(`{"type":"doc","content":[]}`))
		if got != "" {
			t.Errorf("extractProseMirror() = %q, want empty", got)
		}
	})

	t.Run("null input", func(t *testing.T) {
		got := extractProseMirror(json.RawMessage(`null`))
		if got != "" {
			t.Errorf("extractProseMirror() = %q, want empty", got)
		}
	})

	t.Run("double-encoded JSON", func(t *testing.T) {
		inner := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Nested"}]}]}`
		b, _ := json.Marshal(inner)
		got := extractProseMirror(b)
		want := "Nested\n"
		if got != want {
			t.Errorf("extractProseMirror() = %q, want %q", got, want)
		}
	})

	t.Run("heading and paragraph", func(t *testing.T) {
		input := json.RawMessage(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Section"}]},{"type":"paragraph","content":[{"type":"text","text":"Body text"}]}]}`)
		got := extractProseMirror(input)
		want := "## Section\n\nBody text\n"
		if got != want {
			t.Errorf("extractProseMirror() = %q, want %q", got, want)
		}
	})
}

func TestExtractHTML(t *testing.T) {
	t.Run("headings and lists", func(t *testing.T) {
		input := json.RawMessage(`"<h3>Candidate Background</h3><ul><li>Item one</li><li>Item two</li></ul><p>Body text</p>"`)
		got := ExtractHTML(input)
		if !contains(got, "Candidate Background") {
			t.Errorf("ExtractHTML() missing heading text: %q", got)
		}
		if !contains(got, "Item one") {
			t.Errorf("ExtractHTML() missing list item: %q", got)
		}
		if !contains(got, "Body text") {
			t.Errorf("ExtractHTML() missing paragraph: %q", got)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		got := ExtractHTML(json.RawMessage(`""`))
		if got != "" {
			t.Errorf("ExtractHTML() = %q, want empty", got)
		}
	})

	t.Run("non-HTML JSON", func(t *testing.T) {
		got := ExtractHTML(json.RawMessage(`"just plain text"`))
		if got != "just plain text\n" {
			t.Errorf("ExtractHTML() = %q, want %q", got, "just plain text\n")
		}
	})

	t.Run("null input", func(t *testing.T) {
		got := ExtractHTML(json.RawMessage(`null`))
		if got != "" {
			t.Errorf("ExtractHTML() = %q, want empty", got)
		}
	})
}

func TestApplyMarks(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		marks []ProseMirrorMark
		want  string
	}{
		{
			name: "bold",
			text: "bold text",
			marks: []ProseMirrorMark{{Type: "bold"}},
			want: "**bold text**",
		},
		{
			name: "italic",
			text: "italic text",
			marks: []ProseMirrorMark{{Type: "italic"}},
			want: "*italic text*",
		},
		{
			name: "code",
			text: "code",
			marks: []ProseMirrorMark{{Type: "code"}},
			want: "`code`",
		},
		{
			name: "link",
			text: "click here",
			marks: []ProseMirrorMark{{
				Type:  "link",
				Attrs: map[string]interface{}{"href": "https://example.com"},
			}},
			want: "[click here](https://example.com)",
		},
		{
			name: "strikethrough",
			text: "old text",
			marks: []ProseMirrorMark{{Type: "strike"}},
			want: "~~old text~~",
		},
		{
			name:  "no marks",
			text:  "plain",
			marks: nil,
			want:  "plain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyMarks(tt.text, tt.marks)
			if got != tt.want {
				t.Errorf("applyMarks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTranscript(t *testing.T) {
	t.Run("single speaker", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: Hello", StartTimestamp: "2026-01-15T16:00:00Z"},
			{Text: "Speaker 1: World", StartTimestamp: "2026-01-15T16:00:00Z"},
		}
		got := FormatTranscript(segments)
		want := "Speaker 1: Hello\n\nSpeaker 1: World"
		if got != want {
			t.Errorf("FormatTranscript() = %q, want %q", got, want)
		}
	})

	t.Run("multiple speakers with timestamps", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: First", StartTimestamp: "2026-01-15T16:00:00Z"},
			{Text: "Speaker 2: Second", StartTimestamp: "2026-01-15T16:01:00Z"},
		}
		got := FormatTranscript(segments)
		want := "[16:00:00] Speaker 1: First\n\n[16:01:00] Speaker 2: Second"
		if got != want {
			t.Errorf("FormatTranscript() = %q, want %q", got, want)
		}
	})

	t.Run("empty segments", func(t *testing.T) {
		got := FormatTranscript(nil)
		if got != "" {
			t.Errorf("FormatTranscript() = %q, want empty", got)
		}
	})

	t.Run("continuing speaker", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: Line one", StartTimestamp: "2026-01-15T16:00:00Z"},
			{Text: "Still speaker one", StartTimestamp: "2026-01-15T16:00:00Z"},
		}
		got := FormatTranscript(segments)
		want := "Speaker 1: Line one\n\nSpeaker 1: Still speaker one"
		if got != want {
			t.Errorf("FormatTranscript() = %q, want %q", got, want)
		}
	})
}

func TestFormatTranscriptSameTimestamp(t *testing.T) {
	segments := []TranscriptSegment{
		{Text: "Speaker 1: Hello", StartTimestamp: "2026-01-15T16:00:00Z"},
		{Text: "Speaker 2: Hi", StartTimestamp: "2026-01-15T16:00:00Z"},
	}
	got := FormatTranscript(segments)
	if contains(got, "[") {
		t.Errorf("FormatTranscript() should omit timestamps when all same: %q", got)
	}
}

func TestExtractNotes(t *testing.T) {
	t.Run("notes_markdown priority", func(t *testing.T) {
		md := "# Hello"
		doc := APIDocument{
			NotesMarkdown: &md,
			NotesPlain:    strPtr("plain"),
		}
		got := ExtractNotes(&doc)
		if got != md {
			t.Errorf("ExtractNotes() = %q, want %q", got, md)
		}
	})

	t.Run("notes_plain fallback", func(t *testing.T) {
		plain := "plain notes"
		doc := APIDocument{
			NotesPlain: &plain,
		}
		got := ExtractNotes(&doc)
		if got != plain {
			t.Errorf("ExtractNotes() = %q, want %q", got, plain)
		}
	})

	t.Run("no notes", func(t *testing.T) {
		doc := APIDocument{}
		got := ExtractNotes(&doc)
		if got != "" {
			t.Errorf("ExtractNotes() = %q, want empty", got)
		}
	})
}

func TestSupabasePath(t *testing.T) {
	t.Run("APPDATA set", func(t *testing.T) {
		prev := os.Getenv("APPDATA")
		os.Setenv("APPDATA", `C:\TestAppData`)
		defer os.Setenv("APPDATA", prev)

		got := supabasePath()
		want := `C:\TestAppData\Granola`
		if got != want {
			t.Errorf("supabasePath() = %q, want %q", got, want)
		}
	})

	t.Run("APPDATA empty falls back to USERPROFILE", func(t *testing.T) {
		prevAppData := os.Getenv("APPDATA")
		prevUserProfile := os.Getenv("USERPROFILE")
		os.Setenv("APPDATA", "")
		os.Setenv("USERPROFILE", `C:\Users\Test`)
		defer os.Setenv("APPDATA", prevAppData)
		defer os.Setenv("USERPROFILE", prevUserProfile)

		got := supabasePath()
		want := `C:\Users\Test\AppData\Roaming\Granola`
		if got != want {
			t.Errorf("supabasePath() = %q, want %q", got, want)
		}
	})
}

func TestExtractDateFromDoc(t *testing.T) {
	t.Run("valid RFC3339", func(t *testing.T) {
		doc := APIDocument{CreatedAt: "2026-01-15T16:00:00Z"}
		got := ExtractDateFromDoc(doc)
		if got.Year() != 2026 || got.Month() != 1 || got.Day() != 15 {
			t.Errorf("ExtractDateFromDoc() = %v, want 2026-01-15", got)
		}
	})

	t.Run("invalid date", func(t *testing.T) {
		doc := APIDocument{CreatedAt: "not-a-date"}
		got := ExtractDateFromDoc(doc)
		if got.IsZero() {
			t.Error("ExtractDateFromDoc() should return non-zero time on parse failure")
		}
	})
}

func TestAPIPostSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	originalBase := granolaAPIBase
	granolaAPIBase = srv.URL
	defer func() { granolaAPIBase = originalBase }()

	var result map[string]string
	err := Post("test-token", "/test", nil, &result)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestAPIPostUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer srv.Close()

	originalBase := granolaAPIBase
	granolaAPIBase = srv.URL
	defer func() { granolaAPIBase = originalBase }()

	err := Post("bad-token", "/test", nil, nil)
	if err == nil {
		t.Fatal("Post() expected error for 401")
	}
}

func TestAPIPostRetryOnServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	originalBase := granolaAPIBase
	originalRetries := maxRetries
	granolaAPIBase = srv.URL
	maxRetries = 3
	defer func() {
		granolaAPIBase = originalBase
		maxRetries = originalRetries
	}()

	var result map[string]string
	err := Post("test-token", "/test", nil, &result)
	if err != nil {
		t.Fatalf("Post() error after retry = %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRefreshCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
		})
	}))
	defer srv.Close()

	originalURL := workosAuthURL
	workosAuthURL = srv.URL
	defer func() { workosAuthURL = originalURL }()

	creds := &Credentials{
		AccessToken:  "old-token",
		RefreshToken: "refresh-token",
		ClientID:     "test-client",
	}
	refreshed, err := RefreshCredentials(creds)
	if err != nil {
		t.Fatalf("RefreshCredentials() error = %v", err)
	}
	if refreshed.AccessToken != "new-access-token" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "new-access-token")
	}
	if refreshed.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "new-refresh-token")
	}
}

func TestFetchDocumentDetail(t *testing.T) {
	detailDoc := APIDocument{
		ID:            "detail-1",
		Title:         strPtr("Detail Meeting"),
		CreatedAt:     "2026-06-12T10:00:00Z",
		NotesMarkdown: strPtr("# Detail notes\n\nFrom detail endpoint"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req DocumentDetailRequest
		json.NewDecoder(r.Body).Decode(&req)
		r.Body.Close()

		if req.DocumentID == "detail-1" {
			json.NewEncoder(w).Encode(DocumentDetailResponse{Doc: detailDoc})
		} else {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
	defer srv.Close()

	originalBase := granolaAPIBase
	granolaAPIBase = srv.URL
	defer func() { granolaAPIBase = originalBase }()

	t.Run("existing document", func(t *testing.T) {
		doc, err := FetchDocumentDetail("test-token", "detail-1")
		if err != nil {
			t.Fatalf("FetchDocumentDetail() error = %v", err)
		}
		if doc.ID != "detail-1" {
			t.Errorf("ID = %q, want %q", doc.ID, "detail-1")
		}
		if doc.NotesMarkdown == nil || *doc.NotesMarkdown != "# Detail notes\n\nFrom detail endpoint" {
			t.Errorf("NotesMarkdown = %v, want %q", doc.NotesMarkdown, "# Detail notes\n\nFrom detail endpoint")
		}
	})

	t.Run("missing document", func(t *testing.T) {
		_, err := FetchDocumentDetail("test-token", "nonexistent")
		if err == nil {
			t.Fatal("FetchDocumentDetail() expected error for nonexistent doc")
		}
	})
}

func TestFetchAllDocumentsAfter(t *testing.T) {
	docs := []APIDocument{
		{ID: "1", CreatedAt: "2026-06-01T00:00:00Z"},
		{ID: "2", CreatedAt: "2026-06-02T00:00:00Z"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req DocumentsRequest
		json.NewDecoder(r.Body).Decode(&req)
		r.Body.Close()

		if req.Offset >= len(docs) {
			json.NewEncoder(w).Encode(DocumentsResponse{Docs: []APIDocument{}})
			return
		}
		json.NewEncoder(w).Encode(DocumentsResponse{Docs: docs[req.Offset:]})
	}))
	defer srv.Close()

	originalBase := granolaAPIBase
	originalLimit := defaultLimit
	granolaAPIBase = srv.URL
	defaultLimit = 10
	defer func() {
		granolaAPIBase = originalBase
		defaultLimit = originalLimit
	}()

	after := "2026-01-01T00:00:00Z"
	all, err := FetchAllDocumentsAfter("test-token", &after)
	if err != nil {
		t.Fatalf("FetchAllDocumentsAfter() error = %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d docs, want 2", len(all))
	}
}

func TestParseTime(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		tm, err := ParseTime("2026-06-17T17:30:19Z")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if tm.Year() != 2026 || tm.Month() != 6 || tm.Day() != 17 {
			t.Errorf("ParseTime() = %v, want 2026-06-17", tm)
		}
	})

	t.Run("RFC3339Nano with fractional seconds", func(t *testing.T) {
		tm, err := ParseTime("2026-06-17T17:30:19.123456Z")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if tm.Nanosecond() != 123456000 {
			t.Errorf("ParseTime() nanoseconds = %d, want 123456000", tm.Nanosecond())
		}
	})

	t.Run("RFC3339 with timezone offset", func(t *testing.T) {
		tm, err := ParseTime("2026-06-17T17:30:19+00:00")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if tm.Year() != 2026 {
			t.Errorf("ParseTime() = %v, want 2026", tm)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := ParseTime("")
		if err == nil {
			t.Fatal("ParseTime() expected error for empty string")
		}
	})
}

func TestMeetingTimeRange(t *testing.T) {
	t.Run("both timestamps present", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: Hello", StartTimestamp: "2026-01-15T16:00:00Z", EndTimestamp: "2026-01-15T16:30:00Z"},
			{Text: "Speaker 2: World", StartTimestamp: "2026-01-15T16:30:00Z", EndTimestamp: "2026-01-15T17:00:00Z"},
		}
		end, dur := MeetingTimeRange(segments, time.Time{})
		if end.IsZero() {
			t.Error("MeetingTimeRange() returned zero end time")
		}
		if dur != 1*time.Hour {
			t.Errorf("MeetingTimeRange() duration = %v, want 1h0m0s", dur)
		}
	})

	t.Run("missing EndTimestamp falls back to StartTimestamp", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: Hello", StartTimestamp: "2026-01-15T16:00:00Z"},
			{Text: "Speaker 2: World", StartTimestamp: "2026-01-15T16:30:00Z"},
		}
		end, dur := MeetingTimeRange(segments, time.Time{})
		if end.IsZero() {
			t.Error("MeetingTimeRange() returned zero end time with missing EndTimestamp")
		}
		if dur != 30*time.Minute {
			t.Errorf("MeetingTimeRange() duration = %v, want 30m0s", dur)
		}
	})

	t.Run("no segments", func(t *testing.T) {
		end, dur := MeetingTimeRange(nil, time.Time{})
		if !end.IsZero() {
			t.Error("MeetingTimeRange() should return zero end time for no segments")
		}
		if dur != 0 {
			t.Errorf("MeetingTimeRange() duration = %v, want 0", dur)
		}
	})

	t.Run("no parseable timestamps", func(t *testing.T) {
		segments := []TranscriptSegment{
			{Text: "Speaker 1: Hello", StartTimestamp: ""},
		}
		end, dur := MeetingTimeRange(segments, time.Time{})
		if !end.IsZero() {
			t.Error("MeetingTimeRange() should return zero end time for no parseable timestamps")
		}
		if dur != 0 {
			t.Errorf("MeetingTimeRange() duration = %v, want 0", dur)
		}
	})
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func strPtr(s string) *string {
	return &s
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
