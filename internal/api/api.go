package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/granola-exporter/granola-backup/internal/encryptedjson"
	"github.com/granola-exporter/granola-backup/internal/redact"
)

var (
	granolaAPIBase    = "https://api.granola.ai"
	workosAuthURL     = "https://api.workos.com/user_management/authenticate"
	defaultClientID   = "client_GranolaMac"
	defaultLimit      = 100
	maxRetries        = 3
	retryBaseDelay    = 250
)

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ExpiresIn    int64  `json:"expires_in"`
	ObtainedAt   int64  `json:"obtained_at"`
}

func (c Credentials) IsExpired() bool {
	if c.ObtainedAt == 0 || c.ExpiresIn == 0 {
		return true
	}
	expires := time.UnixMilli(c.ObtainedAt).Add(time.Duration(c.ExpiresIn) * time.Second)
	return time.Now().After(expires)
}

type DocumentsRequest struct {
	Limit                 int     `json:"limit"`
	Offset                int     `json:"offset"`
	IncludeSharedWithMe   bool    `json:"include_shared_with_me"`
	IncludeLastViewedPanel bool   `json:"include_last_viewed_panel,omitempty"`
	CreatedAfter          *string `json:"created_after,omitempty"`
}

type DocumentsResponse struct {
	Docs []APIDocument `json:"docs"`
}

type APIDocument struct {
	ID              string          `json:"id"`
	Title           *string         `json:"title"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	Notes           json.RawMessage `json:"notes"`
	NotesPlain      *string         `json:"notes_plain"`
	NotesMarkdown   *string         `json:"notes_markdown"`
	LastViewedPanel *PanelData      `json:"last_viewed_panel"`
}

type PanelData struct {
	Content          json.RawMessage `json:"content"`
	ContentUpdatedAt string          `json:"content_updated_at"`
}

type TranscriptRequest struct {
	DocumentID string `json:"document_id"`
}

type TranscriptSegment struct {
	ID             string `json:"id"`
	DocumentID     string `json:"document_id"`
	StartTimestamp string `json:"start_timestamp"`
	EndTimestamp   string `json:"end_timestamp"`
	Text           string `json:"text"`
	Source         string `json:"source"`
	IsFinal        bool   `json:"is_final"`
}

type ProseMirrorDoc struct {
	Type    string            `json:"type"`
	Content []ProseMirrorNode `json:"content,omitempty"`
}

type ProseMirrorNode struct {
	Type    string                 `json:"type"`
	Content []ProseMirrorNode     `json:"content,omitempty"`
	Text    string                 `json:"text,omitempty"`
	Marks   []ProseMirrorMark      `json:"marks,omitempty"`
	Attrs   map[string]interface{} `json:"attrs,omitempty"`
}

type ProseMirrorMark struct {
	Type  string                 `json:"type"`
	Attrs map[string]interface{} `json:"attrs,omitempty"`
}

type ListInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type DocumentListsResponse struct {
	Lists map[string]DocumentList `json:"lists"`
}

type DocumentList struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	DocumentIDs []string `json:"document_ids"`
}

type DocumentDetailRequest struct {
	DocumentID string `json:"document_id"`
}

type DocumentDetailResponse struct {
	Doc APIDocument `json:"doc"`
}

func ParseCredentialsBytes(b []byte) (*Credentials, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse supabase: %w", err)
	}

	tokensRaw, ok := raw["workos_tokens"].(string)
	if !ok {
		return nil, fmt.Errorf("no workos_tokens in credentials file")
	}

	var creds Credentials
	if err := json.Unmarshal([]byte(tokensRaw), &creds); err != nil {
		return nil, fmt.Errorf("parse workos_tokens: %w", err)
	}

	if creds.ClientID == "" {
		creds.ClientID = ExtractClientID(creds.AccessToken)
	}
	if creds.ClientID == "" {
		creds.ClientID = defaultClientID
	}
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in workos_tokens")
	}
	return &creds, nil
}

func readWindowsCredMan() (*Credentials, error) {
	targets := []string{
		"com.granola.cli",
		"Granola",
	}

	for _, target := range targets {
		creds, err := readCredManEntry(target)
		if err == nil {
			fmt.Printf("  CredMan entry found: %s\n", target)
			return creds, nil
		}
	}
	return nil, fmt.Errorf("no Granola credentials in Credential Manager")
}

func readCredManEntry(target string) (*Credentials, error) {
	script := fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class CredMan {
    [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
    public static extern bool CredReadW(string target, int type, int flags, out IntPtr credential);

    [DllImport("advapi32.dll", SetLastError=true)]
    public static extern void CredFree(IntPtr cred);

    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
    public struct CREDENTIAL {
        public int Flags;
        public int Type;
        public string TargetName;
        public string Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public int CredentialBlobSize;
        public IntPtr CredentialBlobPtr;
        public int Persist;
        public int AttributeCount;
        public IntPtr Attributes;
        public string TargetAlias;
        public string UserName;
    }

    public static byte[] Read(string target) {
        IntPtr ptr;
        if (!CredReadW(target, 1, 0, out ptr)) return null;
        try {
            CREDENTIAL cred = (CREDENTIAL)Marshal.PtrToStructure(ptr, typeof(CREDENTIAL));
            byte[] blob = new byte[cred.CredentialBlobSize];
            Marshal.Copy(cred.CredentialBlobPtr, blob, 0, cred.CredentialBlobSize);
            return blob;
        } finally {
            CredFree(ptr);
        }
    }
}
"@
$blob = [CredMan]::Read('%s')
if ($blob -eq $null) { exit 1 }
[System.Console]::Write([System.Convert]::ToBase64String($blob))
`, target)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	var stdout strings.Builder
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("CredMan: no entry for %s", target)
	}

	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout.String()))
	if err != nil {
		return nil, fmt.Errorf("CredMan decode: %w", err)
	}
	return parseCredManBlob(blob)
}

func parseCredManBlob(blob []byte) (*Credentials, error) {
	var data struct {
		RefreshToken string `json:"refreshToken"`
		AccessToken  string `json:"accessToken"`
		ClientID     string `json:"clientId"`
	}
	if err := json.Unmarshal(blob, &data); err != nil {
		var data2 struct {
			RefreshToken string `json:"refresh_token"`
			AccessToken  string `json:"access_token"`
			ClientID     string `json:"client_id"`
		}
		if err2 := json.Unmarshal(blob, &data2); err2 != nil {
			return nil, fmt.Errorf("parse CredMan blob: %w", err)
		}
		if data2.AccessToken != "" {
			return &Credentials{
				AccessToken:  data2.AccessToken,
				RefreshToken: data2.RefreshToken,
				ClientID:     data2.ClientID,
			}, nil
		}
	}

	if data.RefreshToken == "" && data.AccessToken == "" {
		return nil, fmt.Errorf("CredMan blob: no tokens found")
	}

	creds := &Credentials{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
	}
	if data.ClientID != "" {
		creds.ClientID = data.ClientID
	} else {
		creds.ClientID = ExtractClientID(data.AccessToken)
	}
	if creds.ClientID == "" {
		creds.ClientID = defaultClientID
	}
	return creds, nil
}

func ExtractClientID(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := DecodeBase64URL(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if idx := strings.LastIndex(claims.Iss, "/"); idx >= 0 {
		return claims.Iss[idx+1:]
	}
	return ""
}

func DecodeBase64URL(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

func supabasePath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
	}
	return filepath.Join(appData, "Granola")
}

func FindCredentials() (*Credentials, error) {
	dir := supabasePath()

	lastErr := fmt.Errorf("no credentials found")

	tryCandidates := func(paths []struct{ path, desc string }, readFn func(string) (*Credentials, error)) (*Credentials, error) {
		for _, c := range paths {
			creds, err := readFn(c.path)
			if err != nil {
				lastErr = err
				continue
			}
			fmt.Printf("  Found %s credentials: %s\n", c.desc, c.path)
			return creds, nil
		}
		return nil, lastErr
	}

	creds, err := tryCandidates([]struct{ path, desc string }{
		{filepath.Join(dir, "supabase.json.enc"), "encrypted"},
		{filepath.Join(dir, "supabase.json.enc.bak"), "encrypted backup"},
	}, func(path string) (*Credentials, error) {
		decrypted, err := encryptedjson.DecryptSupabaseJSON(path)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", path, err)
		}
		return ParseCredentialsBytes(decrypted)
	})
	if err == nil {
		return creds, nil
	}

	if runtime.GOOS == "windows" {
		creds, err := readWindowsCredMan()
		if err == nil {
			fmt.Println("  Found credentials via Windows Credential Manager")
			return creds, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

func RefreshCredentials(creds *Credentials) (*Credentials, error) {
	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh_token available")
	}

	body := map[string]string{
		"client_id":     creds.ClientID,
		"grant_type":    "refresh_token",
		"refresh_token": creds.RefreshToken,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", workosAuthURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("refresh read: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("refresh failed: status=%d body=%s", resp.StatusCode, redact.String(string(respBytes)))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("refresh parse: %w", err)
	}

	return &Credentials{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ClientID:     creds.ClientID,
		ObtainedAt:   time.Now().UnixMilli(),
		ExpiresIn:    3600,
	}, nil
}

func Post(token, endpoint string, input, output interface{}) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		var bodyReader io.Reader
		if input != nil {
			b, err := json.Marshal(input)
			if err != nil {
				return err
			}
			bodyReader = strings.NewReader(string(b))
		}
		req, err := http.NewRequest("POST", granolaAPIBase+endpoint, bodyReader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Client-Version", "auto")
		req.Header.Set("X-Granola-Platform", "windows")
		req.Header.Set("User-Agent", "Granola-Backup/1.0")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(retryBaseDelay*(1<<attempt)) * time.Millisecond)
			continue
		}

		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("api error: status=%d body=%s", resp.StatusCode, redact.String(string(b)))
			if resp.StatusCode == 401 {
				return lastErr
			}
			time.Sleep(time.Duration(retryBaseDelay*(1<<attempt)) * time.Millisecond)
			continue
		}
		if output == nil {
			return nil
		}
		return json.Unmarshal(b, output)
	}
	return lastErr
}

func FetchAllDocumentsAfter(token string, createdAfter *string) ([]APIDocument, error) {
	var all []APIDocument
	offset := 0

	for {
		req := DocumentsRequest{
			Limit:                 defaultLimit,
			Offset:                offset,
			IncludeSharedWithMe:   false,
			IncludeLastViewedPanel: true,
			CreatedAfter:          createdAfter,
		}

		var resp DocumentsResponse
		err := Post(token, "/v2/get-documents", req, &resp)
		if err != nil {
			return nil, fmt.Errorf("get-documents offset=%d: %w", offset, err)
		}

		all = append(all, resp.Docs...)
		fmt.Printf("  Fetched %d docs (offset=%d, total=%d)\n", len(resp.Docs), offset, len(all))

		if len(resp.Docs) < defaultLimit {
			break
		}
		offset += defaultLimit
	}

	return all, nil
}

func ExtractNotes(doc *APIDocument) string {
	if doc.NotesMarkdown != nil && *doc.NotesMarkdown != "" {
		return *doc.NotesMarkdown
	}
	if doc.NotesPlain != nil && *doc.NotesPlain != "" {
		return *doc.NotesPlain
	}
	if doc.Notes != nil && string(doc.Notes) != "null" && len(doc.Notes) > 0 {
		text := extractProseMirror(doc.Notes)
		if text != "" {
			return text
		}
	}
	if doc.LastViewedPanel != nil && doc.LastViewedPanel.Content != nil {
		text := extractProseMirror(doc.LastViewedPanel.Content)
		if text != "" {
			return text
		}
		text = ExtractHTML(doc.LastViewedPanel.Content)
		if text != "" {
			return text
		}
	}
	return ""
}

func extractProseMirror(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var doc ProseMirrorDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return ""
		}
		if err := json.Unmarshal([]byte(str), &doc); err != nil {
			return ""
		}
	}

	if doc.Type != "doc" || len(doc.Content) == 0 {
		return ""
	}

	var out []string
	for _, node := range doc.Content {
		out = append(out, pmNodeToMD(node, 0, true))
	}
	result := strings.Join(out, "")
	result = strings.TrimSpace(result)
	if result != "" {
		result += "\n"
	}
	return result
}

func pmNodeToMD(node ProseMirrorNode, indent int, topLevel bool) string {
	if node.Type == "text" {
		return applyMarks(node.Text, node.Marks)
	}

	var content string
	if len(node.Content) > 0 {
		var parts []string
		for _, c := range node.Content {
			parts = append(parts, pmNodeToMD(c, indent, false))
		}
		content = strings.Join(parts, "")
	}

	switch node.Type {
	case "heading":
		level := 1
		if node.Attrs != nil {
			if l, ok := node.Attrs["level"].(float64); ok {
				level = int(l)
			}
		}
		prefix := strings.Repeat("#", level) + " "
		suffix := "\n\n"
		return prefix + strings.TrimSpace(content) + suffix

	case "paragraph":
		suffix := "\n\n"
		return content + suffix

	case "bulletList":
		var items []string
		for _, c := range node.Content {
			if c.Type == "listItem" {
				var childTexts []string
				var nested []string
				for _, child := range c.Content {
					if child.Type == "bulletList" || child.Type == "orderedList" {
						nested = append(nested, pmNodeToMD(child, indent+1, false))
					} else {
						childTexts = append(childTexts, strings.TrimSpace(pmNodeToMD(child, indent, false)))
					}
				}
				item := strings.Repeat("  ", indent) + "- " + strings.Join(childTexts, " ")
				if len(nested) > 0 {
					item += "\n" + strings.Join(nested, "\n")
				}
				items = append(items, item)
			}
		}
		return strings.Join(items, "\n") + "\n\n"

	case "orderedList":
		var items []string
		for idx, c := range node.Content {
			if c.Type == "listItem" {
				var childTexts []string
				for _, child := range c.Content {
					if child.Type != "bulletList" && child.Type != "orderedList" {
						childTexts = append(childTexts, strings.TrimSpace(pmNodeToMD(child, indent, false)))
					}
				}
				items = append(items, fmt.Sprintf("%s%d. %s", strings.Repeat("  ", indent), idx+1, strings.Join(childTexts, " ")))
			}
		}
		return strings.Join(items, "\n") + "\n\n"

	case "listItem":
		return content

	case "blockquote":
		quoted := strings.TrimSpace(content)
		lines := strings.Split(quoted, "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return strings.Join(lines, "\n") + "\n\n"

	case "codeBlock":
		lang := ""
		if node.Attrs != nil {
			if l, ok := node.Attrs["language"].(string); ok {
				lang = l
			}
		}
		return "```" + lang + "\n" + strings.TrimSpace(content) + "\n```\n\n"

	case "horizontalRule":
		return "---\n\n"

	default:
		return content
	}
}

var (
	reHeadings = regexp.MustCompile(`(?i)</h([1-6])\s*>`)
	reTag      = regexp.MustCompile(`<[^>]*>`)
	reBr       = regexp.MustCompile(`(?i)<br\s*/?>`)
	reNewlines = regexp.MustCompile(`\n{3,}`)
)

func ExtractHTML(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	s = reBr.ReplaceAllString(s, "\n")

	s = reHeadings.ReplaceAllStringFunc(s, func(match string) string {
		level := match[len(match)-2] - '0'
		return "\n\n" + strings.Repeat("#", int(level)) + " "
	})

	s = strings.ReplaceAll(s, "<li>", "\n- ")
	s = strings.ReplaceAll(s, "</li>", "")
	s = strings.ReplaceAll(s, "</ul>", "\n")
	s = strings.ReplaceAll(s, "</ol>", "\n")

	s = strings.ReplaceAll(s, "</p>", "\n\n")

	s = reTag.ReplaceAllString(s, "")

	s = html.UnescapeString(s)

	s = reNewlines.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if s != "" {
		s += "\n"
	}
	return s
}

func applyMarks(text string, marks []ProseMirrorMark) string {
	if text == "" {
		return ""
	}
	result := text
	for _, m := range marks {
		switch m.Type {
		case "bold", "strong":
			result = "**" + result + "**"
		case "italic", "em":
			result = "*" + result + "*"
		case "code":
			result = "`" + result + "`"
		case "strike", "strikethrough":
			result = "~~" + result + "~~"
		case "link":
			href := ""
			if m.Attrs != nil {
				if h, ok := m.Attrs["href"].(string); ok {
					href = h
				}
			}
			if href != "" {
				result = "[" + result + "](" + href + ")"
			}
		}
	}
	return result
}

func FetchDocumentDetail(token, docID string) (*APIDocument, error) {
	req := DocumentDetailRequest{DocumentID: docID}
	var resp DocumentDetailResponse
	err := Post(token, "/v1/get-document", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("get-document %s: %w", docID[:min(len(docID), 8)], err)
	}
	return &resp.Doc, nil
}

func FetchTranscript(token, docID string) ([]TranscriptSegment, error) {
	req := TranscriptRequest{DocumentID: docID}
	var segments []TranscriptSegment
	err := Post(token, "/v1/get-document-transcript", req, &segments)
	if err != nil {
		return nil, fmt.Errorf("get-transcript %s: %w", docID[:min(len(docID), 8)], err)
	}
	return segments, nil
}

func FormatTranscript(segments []TranscriptSegment) string {
	if len(segments) == 0 {
		return ""
	}

	allSame := len(segments) > 1
	if allSame {
		first := segments[0].StartTimestamp
		for _, seg := range segments[1:] {
			if seg.StartTimestamp != first {
				allSame = false
				break
			}
		}
	}

	var lines []string
	var prevSpeaker string

	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}

		speaker := ""
		if strings.HasPrefix(text, "Speaker ") {
			if idx := strings.Index(text, ": "); idx >= 0 {
				speaker = text[:idx]
				text = strings.TrimSpace(text[idx+2:])
			}
		}
		if speaker == "" {
			speaker = prevSpeaker
			if speaker == "" {
				speaker = "Speaker"
			}
		}
		prevSpeaker = speaker

		ts := ""
		if !allSame && seg.StartTimestamp != "" {
			if t, err := time.Parse(time.RFC3339, seg.StartTimestamp); err == nil {
				ts = fmt.Sprintf("[%s] ", t.Format("15:04:05"))
			}
		}

		lines = append(lines, ts+speaker+": "+text)
	}
	return strings.Join(lines, "\n\n")
}

func FetchDocumentLists(token string) (map[string][]ListInfo, error) {
	var resp DocumentListsResponse
	err := Post(token, "/v1/get-document-lists-metadata",
		map[string]interface{}{
			"include_document_ids":      true,
			"include_only_joined_lists": false,
		},
		&resp)
	if err != nil {
		return nil, fmt.Errorf("get-document-lists-metadata: %w", err)
	}

	mapping := make(map[string][]ListInfo)
	for _, list := range resp.Lists {
		info := ListInfo{ID: list.ID, Title: list.Title}
		for _, docID := range list.DocumentIDs {
			mapping[docID] = append(mapping[docID], info)
		}
	}

	for docID, lists := range mapping {
		sort.Slice(lists, func(i, j int) bool {
			if lists[i].Title != lists[j].Title {
				return lists[i].Title < lists[j].Title
			}
			return lists[i].ID < lists[j].ID
		})
		mapping[docID] = lists
	}

	return mapping, nil
}

func ExtractDateFromDoc(doc APIDocument) time.Time {
	t, err := time.Parse(time.RFC3339, doc.CreatedAt)
	if err == nil {
		return t
	}
	return time.Now()
}

func ExtractCredentialsFromEncryptedFile(encPath string) (*Credentials, error) {
	decrypted, err := encryptedjson.DecryptSupabaseJSON(encPath)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", encPath, err)
	}
	return ParseCredentialsBytes(decrypted)
}

func ExtractCredentialsToFile(outPath string) error {
	creds, err := FindCredentials()
	if err != nil {
		return fmt.Errorf("find credentials: %w", err)
	}

	output := struct {
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
	}{
		RefreshToken: creds.RefreshToken,
		ClientID:     creds.ClientID,
	}

	b, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(outPath, b, 0600); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}
