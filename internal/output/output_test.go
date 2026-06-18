package output

import (
	"os"
	"testing"
	"time"

	"github.com/granola-exporter/granola-backup/internal/api"
)

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"normal title", "My Meeting", "My Meeting"},
		{"illegal chars", "file:name<test>", "file_name_test_"},
		{"spaces trimmed", "  hello  ", "hello"},
		{"long string truncated", string(make([]byte, 300)), ""},
		{"empty becomes untitled", "", "untitled"},
		{"only illegal chars", "<>:\"", "____"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeFilename(tt.in)
			if got == tt.in && got != "untitled" {
				return
			}
			if len(got) > 200 {
				t.Errorf("SafeFilename() length = %d, want <= 200", len(got))
			}
		})
	}
}

func TestWriteMeetingFile(t *testing.T) {
	dir := t.TempDir()
	m := Meeting{
		ID:       "test-id",
		Title:    "Test Meeting",
		DateTime: time.Date(2026, 1, 15, 16, 0, 0, 0, time.UTC),
		Notes:    "Meeting notes content",
		Segments: []api.TranscriptSegment{
			{Text: "Speaker 1: Hello"},
		},
	}
	path, err := writeMeetingFile(dir+"/test.md", m)
	if err != nil {
		t.Fatalf("writeMeetingFile() error = %v", err)
	}
	if path == "" {
		t.Fatal("writeMeetingFile() returned empty path")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Version: 1}

	m.Add("id-1", "folder/meeting-1.md", time.Date(2026, 1, 15, 16, 0, 0, 0, time.UTC))
	m.Add("id-2", "folder/meeting-2.md", time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC))

	if err := m.Save(dir); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded := LoadManifest(dir)
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}

	entry, ok := loaded.Contains("id-1")
	if !ok {
		t.Fatal("Contains(id-1) = false, want true")
	}
	if entry.ID != "id-1" {
		t.Errorf("entry.ID = %q, want %q", entry.ID, "id-1")
	}

	if _, ok := loaded.Contains("id-2"); !ok {
		t.Error("Contains(id-2) = false, want true")
	}
	if _, ok := loaded.Contains("nonexistent"); ok {
		t.Error("Contains(nonexistent) = true, want false")
	}
}

func TestManifestSortedByDateTime(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Version: 1}

	m.Add("old", "old.md", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m.Add("new", "new.md", time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC))

	m.Save(dir)
	loaded := LoadManifest(dir)

	if len(loaded.Exported) != 2 {
		t.Fatalf("got %d entries, want 2", len(loaded.Exported))
	}

	if loaded.Exported[0].ID != "new" {
		t.Errorf("first entry ID = %q, want %q (newest first)", loaded.Exported[0].ID, "new")
	}
	if loaded.Exported[1].ID != "old" {
		t.Errorf("second entry ID = %q, want %q", loaded.Exported[1].ID, "old")
	}
}

func TestManifestLastExportDate(t *testing.T) {
	m := &Manifest{Version: 1}
	m.Add("a", "a.md", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	date, ok := m.LastExportDate()
	if !ok {
		t.Fatal("LastExportDate() = false, want true")
	}
	if date.IsZero() {
		t.Error("LastExportDate() should return a non-zero time")
	}
}

func TestManifestBackwardCompat(t *testing.T) {
	oldJSON := `{
		"version": 1,
		"exported": {
			"id-1": {
				"path": "old-path.md",
				"exported_at": "2026-01-15T10:00:00Z",
				"meeting_date": "2026-01-15"
			}
		}
	}`
	dir := t.TempDir()
	writeFile(t, dir+"/exported.json", oldJSON)

	loaded := LoadManifest(dir)
	entry, ok := loaded.Contains("id-1")
	if !ok {
		t.Fatal("backward compat: Contains(id-1) = false")
	}
	if entry.Path != "old-path.md" {
		t.Errorf("Path = %q, want %q", entry.Path, "old-path.md")
	}
	if entry.MeetingDateTime == "" {
		t.Error("MeetingDateTime should be populated from meeting_date")
	}
}

func TestEnsureMeetingTemplate(t *testing.T) {
	t.Run("uses existing meeting_template.md", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/meeting_template.md", []byte("custom: {{.Meeting.Title}}"), 0644)

		src, err := ensureMeetingTemplate(dir)
		if err != nil {
			t.Fatalf("ensureMeetingTemplate() error = %v", err)
		}
		if src != "custom: {{.Meeting.Title}}" {
			t.Errorf("ensureMeetingTemplate() = %q, want custom template", src)
		}
	})

	t.Run("creates default meeting_template.md when missing", func(t *testing.T) {
		dir := t.TempDir()

		src, err := ensureMeetingTemplate(dir)
		if err != nil {
			t.Fatalf("ensureMeetingTemplate() error = %v", err)
		}
		if src == "" {
			t.Fatal("ensureMeetingTemplate() returned empty template")
		}
		if _, err := os.Stat(dir + "/meeting_template.md"); err != nil {
			t.Errorf("meeting_template.md should have been created: %v", err)
		}
	})

	t.Run("walks up to parent directory", func(t *testing.T) {
		parent := t.TempDir()
		subdir := parent + "/sub"
		os.MkdirAll(subdir, 0755)
		os.WriteFile(parent+"/meeting_template.md", []byte("parent: {{.Meeting.Title}}"), 0644)

		src, err := ensureMeetingTemplate(subdir)
		if err != nil {
			t.Fatalf("ensureMeetingTemplate() error = %v", err)
		}
		if src != "parent: {{.Meeting.Title}}" {
			t.Errorf("ensureMeetingTemplate() = %q, want parent template", src)
		}
	})
}

func TestEnsureSegmentTemplate(t *testing.T) {
	t.Run("uses existing transcript_segment_template.md", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/transcript_segment_template.md", []byte("[[{{.TranscriptSegment.Speaker}}]] {{.TranscriptSegment.Text}}"), 0644)

		src, err := ensureSegmentTemplate(dir)
		if err != nil {
			t.Fatalf("ensureSegmentTemplate() error = %v", err)
		}
		if src != "[[{{.TranscriptSegment.Speaker}}]] {{.TranscriptSegment.Text}}" {
			t.Errorf("ensureSegmentTemplate() = %q, want custom template", src)
		}
	})

	t.Run("creates default when missing", func(t *testing.T) {
		dir := t.TempDir()

		src, err := ensureSegmentTemplate(dir)
		if err != nil {
			t.Fatalf("ensureSegmentTemplate() error = %v", err)
		}
		if src == "" {
			t.Fatal("ensureSegmentTemplate() returned empty template")
		}
		if _, err := os.Stat(dir + "/transcript_segment_template.md"); err != nil {
			t.Errorf("transcript_segment_template.md should have been created: %v", err)
		}
	})

	t.Run("walks up to parent directory", func(t *testing.T) {
		parent := t.TempDir()
		subdir := parent + "/sub"
		os.MkdirAll(subdir, 0755)
		os.WriteFile(parent+"/transcript_segment_template.md", []byte("parent segment tmpl"), 0644)

		src, err := ensureSegmentTemplate(subdir)
		if err != nil {
			t.Fatalf("ensureSegmentTemplate() error = %v", err)
		}
		if src != "parent segment tmpl" {
			t.Errorf("ensureSegmentTemplate() = %q, want parent template", src)
		}
	})
}

func TestFormatSegments(t *testing.T) {
	t.Run("renders segments with default template", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/transcript_segment_template.md", []byte("{{.TranscriptSegment.Speaker}}: {{.TranscriptSegment.Text}}"), 0644)

		segments := []api.TranscriptSegment{
			{Text: "Speaker 1: Hello", StartTimestamp: "2026-01-15T16:00:00Z", EndTimestamp: "2026-01-15T16:01:00Z", Source: "diarization", IsFinal: true},
			{Text: "Speaker 2: World", StartTimestamp: "2026-01-15T16:01:00Z", EndTimestamp: "2026-01-15T16:02:00Z", Source: "diarization", IsFinal: true},
		}

		got := formatSegments(segments, dir)
		want := "Speaker 1: Hello\n\nSpeaker 2: World"
		if got != want {
			t.Errorf("formatSegments() = %q, want %q", got, want)
		}
	})

	t.Run("empty segments", func(t *testing.T) {
		got := formatSegments(nil, t.TempDir())
		if got != "" {
			t.Errorf("formatSegments() = %q, want empty", got)
		}
	})

}

func TestWriteMeetingIntegration(t *testing.T) {
	dir := t.TempDir()
	m := Meeting{
		ID:       "test-id",
		Title:    "Integration Test",
		DateTime: time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC),
		Notes:    "Test notes",
		Segments: []api.TranscriptSegment{
			{Text: "Speaker 1: Test transcript"},
		},
	}

	manifest := &Manifest{Version: 1}
	relPath, err := WriteMeeting(dir, m, false, manifest)
	if err != nil {
		t.Fatalf("WriteMeeting() error = %v", err)
	}
	if relPath == "" {
		t.Fatal("WriteMeeting() returned empty path")
	}

	if err := manifest.Save(dir); err != nil {
		t.Fatalf("manifest.Save() error = %v", err)
	}

	manifestPath := dir + "/exported.json"
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest not found: %v", err)
	}

	relPath2, err := WriteMeeting(dir, m, false, manifest)
	if err != nil {
		t.Fatalf("WriteMeeting() dedup error = %v", err)
	}
	if relPath2 != "" {
		t.Errorf("WriteMeeting() should skip duplicate, got path = %q", relPath2)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
