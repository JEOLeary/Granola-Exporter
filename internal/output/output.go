package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/granola-exporter/granola-backup/internal/api"
)

const defaultTemplate = `# {{.Title}}

Date/Time: {{.DateTimeFormatted}}

Meeting ID: {{.MeetingID}}

## Notes

{{if .Notes}}{{.Notes}}{{else}}*No notes*{{end}}

---

## Transcript

{{if .Transcript}}{{.Transcript}}{{else}}*No transcript*{{end}}`

type Meeting struct {
	ID         string
	Title      string
	DateTime   time.Time
	Notes      string
	Transcript string
	Lists      []api.ListInfo
}

type ManifestEntry struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	ExportedAt      string `json:"exported_at"`
	MeetingDateTime string `json:"meeting_datetime,omitempty"`
}

type Manifest struct {
	Version  int             `json:"version"`
	Exported []ManifestEntry `json:"exported"`
}

func LoadManifest(dir string) *Manifest {
	path := filepath.Join(dir, "exported.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return &Manifest{Version: 1}
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil || m.Exported == nil {
		return &Manifest{Version: 1}
	}
	return &m
}

func sortKey(e ManifestEntry) string {
	return e.MeetingDateTime
}

func (m *Manifest) Save(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	sort.Slice(m.Exported, func(i, j int) bool {
		return sortKey(m.Exported[i]) > sortKey(m.Exported[j])
	})
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "exported.json"), b, 0644)
}

func (m *Manifest) Contains(id string) (ManifestEntry, bool) {
	for _, e := range m.Exported {
		if e.ID == id {
			return e, true
		}
	}
	return ManifestEntry{}, false
}

func (m *Manifest) Add(id, relPath string, dt time.Time) {
	m.Exported = append(m.Exported, ManifestEntry{
		ID:              id,
		Path:            relPath,
		ExportedAt:      time.Now().UTC().Format(time.RFC3339),
		MeetingDateTime: dt.UTC().Format(time.RFC3339),
	})
}

func (m *Manifest) UnmarshalJSON(b []byte) error {
	type entryJSON struct {
		ID              string `json:"id"`
		Path            string `json:"path"`
		ExportedAt      string `json:"exported_at"`
		MeetingDate     string `json:"meeting_date,omitempty"`
		MeetingDateTime string `json:"meeting_datetime,omitempty"`
	}
	toEntry := func(e entryJSON) ManifestEntry {
		dt := e.MeetingDateTime
		if dt == "" {
			dt = e.MeetingDate
		}
		return ManifestEntry{
			ID:              e.ID,
			Path:            e.Path,
			ExportedAt:      e.ExportedAt,
			MeetingDateTime: dt,
		}
	}

	var arr struct {
		Version  int         `json:"version"`
		Exported []entryJSON `json:"exported"`
	}
	if err := json.Unmarshal(b, &arr); err == nil && arr.Exported != nil {
		m.Version = arr.Version
		for _, e := range arr.Exported {
			m.Exported = append(m.Exported, toEntry(e))
		}
		sort.Slice(m.Exported, func(i, j int) bool {
			return m.Exported[i].MeetingDateTime > m.Exported[j].MeetingDateTime
		})
		return nil
	}

	var obj struct {
		Version  int                  `json:"version"`
		Exported map[string]entryJSON `json:"exported"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	m.Version = obj.Version
	for id, e := range obj.Exported {
		e.ID = id
		m.Exported = append(m.Exported, toEntry(e))
	}
	sort.Slice(m.Exported, func(i, j int) bool {
		return m.Exported[i].MeetingDateTime > m.Exported[j].MeetingDateTime
	})
	return nil
}

func (m *Manifest) LastExportDate() (time.Time, bool) {
	var latest time.Time
	found := false
	for _, e := range m.Exported {
		t, err := time.Parse(time.RFC3339, e.MeetingDateTime)
		if err != nil {
			continue
		}
		if !found || t.After(latest) {
			latest = t
			found = true
		}
	}
	return latest, found
}

var reSanitize = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
var reDatePrefix = regexp.MustCompile(`^\((\d{4}-\d{2}-\d{2})(?: \d{2}-\d{2}-\d{2})?\)\s.*\.md$`)

func SafeFilename(s string) string {
	s = reSanitize.ReplaceAllString(s, "_")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

func WriteMeeting(dir string, m Meeting, overwrite bool, manifest *Manifest) (string, error) {
	dateStr := m.DateTime.Format("2006-01-02 15-04-05")
	filename := fmt.Sprintf("(%s) %s.md", dateStr, SafeFilename(m.Title))

	var relPath string
	if len(m.Lists) > 0 {
		relPath = filepath.Join(SafeFilename(m.Lists[0].Title), filename)
	} else {
		relPath = filename
	}

	if manifest != nil && m.ID != "" {
		if entry, exists := manifest.Contains(m.ID); exists {
			if !overwrite {
				fmt.Printf("  already exported (%s): %s\n",
					entry.ExportedAt[:10], entry.Path)
				return "", nil
			}
		}
	}

	firstDir := dir
	writePath := filepath.Join(dir, relPath)
	if len(m.Lists) > 0 {
		firstDir = filepath.Join(dir, SafeFilename(m.Lists[0].Title))
		writePath = filepath.Join(firstDir, filename)
	}

	if err := os.MkdirAll(filepath.Dir(writePath), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	if _, err := writeMeetingFile(writePath, m); err != nil {
		return "", err
	}

	for i := 1; i < len(m.Lists); i++ {
		list := m.Lists[i]
		linkDir := filepath.Join(dir, SafeFilename(list.Title))
		if err := os.MkdirAll(linkDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: mkdir %s: %v\n", linkDir, err)
			continue
		}
		linkPath := filepath.Join(linkDir, filename)
		if err := os.Link(writePath, linkPath); err != nil {
			err2 := os.Symlink(writePath, linkPath)
			if err2 != nil {
				input, err3 := os.ReadFile(writePath)
				if err3 != nil {
					fmt.Fprintf(os.Stderr, "  warning: copy %s: %v\n", linkPath, err3)
				} else if err3 := os.WriteFile(linkPath, input, 0644); err3 != nil {
					fmt.Fprintf(os.Stderr, "  warning: write %s: %v\n", linkPath, err3)
				}
			}
		}
	}

	if manifest != nil && m.ID != "" {
		manifest.Add(m.ID, relPath, m.DateTime)
	}

	return relPath, nil
}

type templateData struct {
	Title             string
	DateTimeFormatted string
	MeetingID         string
	Notes             string
	Transcript        string
}

func ensureTemplate(dir string) (string, error) {
	startDir := dir
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "template.md")
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	tmplPath := filepath.Join(startDir, "template.md")
	if err := os.MkdirAll(startDir, 0755); err == nil {
		os.WriteFile(tmplPath, []byte(defaultTemplate), 0644)
	}
	return defaultTemplate, nil
}

func writeMeetingFile(path string, m Meeting) (string, error) {
	notes := strings.TrimSpace(m.Notes)
	transcript := strings.TrimSpace(m.Transcript)

	tmplSrc, err := ensureTemplate(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("template: %w", err)
	}
	tmpl, err := template.New("meeting").Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}

	id := ""
	if m.ID != "" {
		id = m.ID
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateData{
		Title:             m.Title,
		DateTimeFormatted: m.DateTime.Format("Mon Jan 2 15:04:05 2006"),
		MeetingID:         id,
		Notes:             notes,
		Transcript:        transcript,
	}); err != nil {
		return "", fmt.Errorf("template exec: %w", err)
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return path, nil
}

func GetLastExportDate(dir string) (time.Time, bool) {
	manifest := LoadManifest(dir)
	if t, ok := manifest.LastExportDate(); ok {
		return t, true
	}

	latest := time.Time{}
	found := false
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		matches := reDatePrefix.FindStringSubmatch(info.Name())
		if matches == nil {
			return nil
		}
		t, err := time.Parse("2006-01-02", matches[1])
		if err != nil {
			return nil
		}
		if !found || t.After(latest) {
			latest = t
			found = true
		}
		return nil
	})
	return latest, found
}
