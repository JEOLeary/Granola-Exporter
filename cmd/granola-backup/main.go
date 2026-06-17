package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JEOLeary/granola-backup/internal/api"
	"github.com/JEOLeary/granola-backup/internal/cdp"
	"github.com/JEOLeary/granola-backup/internal/output"
)

type refreshTokenFile struct {
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
}

func main() {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	if cfg == nil {
		cfg = &Config{}
	}

	outputDir := flag.String("output", strVal(cfg.Output, "Granola.ai"), "Output directory for markdown files")
	granolaPath := flag.String("granola-path",
		strVal(cfg.GranolaPath, `C:\Users\JEOLeary\AppData\Local\Programs\granola\Granola.exe`),
		"Granola executable path")
	days := flag.Int("days", intVal(cfg.Days, 365), "Number of days of history to fetch")
	sinceStr := flag.String("since", strVal(cfg.Since, ""), "Fetch meetings created after this date (YYYY-MM-DD or RFC3339). Takes priority over -days")
	overwrite := flag.Bool("overwrite", boolVal(cfg.Overwrite, false), "Overwrite existing files")
	debug := flag.Bool("debug", boolVal(cfg.Debug, false), "Enable debug logging")
	exportAll := flag.Bool("export-all", false, "Export all meetings, ignoring last-export manifest")
	exclude := flag.String("exclude", strVal(cfg.Exclude, ""), "Comma-separated folder names to exclude (e.g. \"Personal,Brag Docs\")")
	refreshTokenFile := flag.String("refresh-token-file", strVal(cfg.RefreshTokenFile, ""), "Path to refresh_token.json from granola-backup -extract-token-only (skips all fallback methods)")
	extractTokenOnly := flag.String("extract-token-only", "", "Extract refresh_token.json and exit (no export)")
	login := flag.Bool("login", false, "Authenticate with Granola via browser (writes refresh_token.json)")
	flag.String("config", "", "Path to config file (default: granola-backup.yaml/yml in current dir)")
	flag.Parse()

	var excludeList []string
	if *exclude != "" {
		for _, s := range strings.Split(*exclude, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				excludeList = append(excludeList, s)
			}
		}
	}

	outDir := *outputDir
	if !filepath.IsAbs(outDir) {
		cwd, _ := os.Getwd()
		outDir = filepath.Join(cwd, outDir)
	}

	sinceLastExport := !*exportAll
	if *exportAll {
		*overwrite = true
	}
	createdAfter := computeCreatedAfter(outDir, sinceLastExport, *days, *sinceStr)

	fmt.Println("=== Granola Backup ===")
	fmt.Println("Output:", outDir)
	if createdAfter != nil {
		fmt.Println("Since:", *createdAfter)
	} else {
		fmt.Println("Since: all available")
	}
	fmt.Println("Overwrite:", *overwrite)
	fmt.Println("Debug:", *debug)
	if *exportAll {
		fmt.Println("Export: all meetings")
	}
	if sinceLastExport {
		fmt.Println("Export: since last export (incremental)")
	}
	if configPath != "" {
		fmt.Println("Config:", configPath)
	}
	if *sinceStr != "" {
		fmt.Println("-since:", *sinceStr)
	}
	if len(excludeList) > 0 {
		fmt.Println("Exclude folders:", excludeList)
	}
	fmt.Println()
	if *refreshTokenFile != "" {
		exported, err := exportViaRefreshToken(*refreshTokenFile, outDir, createdAfter, *overwrite, *debug, excludeList)
		if err == nil {
			fmt.Printf("\nExported %d meetings via refresh token.\n", exported)
			return
		}
		fmt.Fprintf(os.Stderr, "Refresh token export failed: %v\n", err)
		os.Exit(1)
	}

	if *extractTokenOnly != "" {
		fmt.Println("Extracting refresh_token.json ...")
		if err := api.ExtractCredentialsToFile(*extractTokenOnly); err != nil {
			fmt.Fprintf(os.Stderr, "Token extraction failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Credentials written to: %s\n", *extractTokenOnly)
		return
	}

	if *login {
		if err := api.BrowserLogin("", "refresh_token.json"); err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// file-based first, CDP fallback
	tryExportViaFileAPI(outDir, createdAfter, *overwrite, *debug, excludeList)
	tryExportViaCDP(*granolaPath, outDir, createdAfter, *overwrite, *debug, excludeList)
}

func tryExportViaFileAPI(outputDir string, createdAfter *string, overwrite, debug bool, exclude []string) {
	fmt.Println("Trying file-based Private API...")
	exported, err := exportViaAPI(outputDir, createdAfter, overwrite, debug, exclude)
	if err == nil {
		fmt.Printf("\nExported %d meetings via Private API.\n", exported)
		os.Exit(0)
	}
	fmt.Printf("  Private API failed: %v\n\n", err)
}

func tryExportViaCDP(granolaPath, outputDir string, createdAfter *string, overwrite, debug bool, exclude []string) {
	fmt.Println("Trying CDP token + Private API...")
	cdpExt := cdp.NewCDPExtractor(granolaPath)
	token, cdpErr := cdpExt.FetchToken()
	if cdpErr == nil {
		exported, apiErr := exportViaToken(token, outputDir, createdAfter, overwrite, debug, exclude)
		if apiErr == nil {
			fmt.Printf("\nExported %d meetings via CDP+API.\n", exported)
			return
		}
		fmt.Printf("  CDP+API failed: %v\n", apiErr)
	} else {
		fmt.Printf("  CDP token: %v\n", cdpErr)
	}

	fmt.Println("\nAll extraction methods failed.")
	fmt.Println("Open Granola, sign in, then run again.")
	os.Exit(1)
}

func computeCreatedAfter(outputDir string, sinceLastExport bool, days int, sinceStr string) *string {
	if sinceStr != "" {
		if t, err := time.Parse("2006-01-02", sinceStr); err == nil {
			after := t.Format(time.RFC3339)
			return &after
		}
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			after := t.Format(time.RFC3339)
			return &after
		}
		fmt.Printf("  Warning: could not parse -since=%q, falling back\n", sinceStr)
	}
	if sinceLastExport {
		lastDate, found := output.GetLastExportDate(outputDir)
		if found {
			after := lastDate.Format(time.RFC3339)
			return &after
		}
		fmt.Println("  No existing exports found, falling back to -days")
	}
	now := time.Now()
	after := now.AddDate(0, 0, -days).Format(time.RFC3339)
	return &after
}

func exportViaToken(token string, outputDir string, createdAfter *string, overwrite bool, debug bool, exclude []string) (int, error) {
	docs, err := api.FetchAllDocumentsAfter(token, createdAfter)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	fmt.Printf("  Found %d documents\n", len(docs))

	listMapping, _ := api.FetchDocumentLists(token)
	if listMapping != nil {
		fmt.Printf("  Found %d folder(s)\n", len(listMapping))
	} else {
		fmt.Println("  No folder mapping available")
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].CreatedAt > docs[j].CreatedAt
	})

	manifest := output.LoadManifest(outputDir)
	return processDocs(docs, listMapping, manifest, token, outputDir, overwrite, debug, exclude), nil
}

func processDocs(docs []api.APIDocument, listMapping map[string][]api.ListInfo, manifest *output.Manifest, token, outputDir string, overwrite bool, debug bool, exclude []string) int {
	var mu sync.Mutex
	exported := 0

	cutoff := len(docs)
	for i, doc := range docs {
		if _, exists := manifest.Contains(doc.ID); exists {
			cutoff = i
			fmt.Printf("  stopping at already-exported: %s\n", doc.ID[:8])
			break
		}
	}
	pending := docs[:cutoff]

	if len(exclude) > 0 {
		var filtered []api.APIDocument
		for _, doc := range pending {
			lists := listMapping[doc.ID]
			if isExcluded(lists, exclude) {
				if debug {
					fmt.Printf("  [debug] %s: excluded by folder filter\n", doc.ID[:8])
				}
				continue
			}
			filtered = append(filtered, doc)
		}
		pending = filtered
	}

	if len(pending) == 0 {
		return 0
	}

	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for _, doc := range pending {
		wg.Add(1)
		doc := doc
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			title := "(untitled)"
			if doc.Title != nil && *doc.Title != "" {
				title = *doc.Title
			}

			dt := api.ExtractDateFromDoc(doc)
			notes := api.ExtractNotes(&doc)

			if notes == "" {
				if debug {
					notesPresent := doc.Notes != nil && string(doc.Notes) != "null" && len(doc.Notes) > 0
					fmt.Printf("  [debug] %s: notes empty. NotesMarkdown=%v NotesPlain=%v Notes=%v LastViewedPanel=%v\n",
						doc.ID[:8],
						doc.NotesMarkdown != nil && *doc.NotesMarkdown != "",
						doc.NotesPlain != nil && *doc.NotesPlain != "",
						notesPresent,
						doc.LastViewedPanel != nil)
					if notesPresent {
						raw := string(doc.Notes)
						if len(raw) > 500 {
							raw = raw[:500]
						}
						fmt.Printf("  [debug] %s: Notes raw (first 500 chars): %s\n", doc.ID[:8], raw)
					}
					if doc.LastViewedPanel != nil && doc.LastViewedPanel.Content != nil {
						raw := string(doc.LastViewedPanel.Content)
						if len(raw) > 500 {
							raw = raw[:500]
						}
						fmt.Printf("  [debug] %s: LastViewedPanel.Content raw (first 500 chars): %s\n", doc.ID[:8], raw)
					}
				}
				detail, err := api.FetchDocumentDetail(token, doc.ID)
				if err == nil && detail != nil {
					notes = api.ExtractNotes(detail)
					if notes != "" && debug {
						fmt.Printf("  [debug] %s: notes recovered from /v1/get-document\n", doc.ID[:8])
					}
				}
			}

			var transcript string
			segments, err := api.FetchTranscript(token, doc.ID)
			if err == nil && len(segments) > 0 {
				transcript = api.FormatTranscript(segments)
			}

			m := output.Meeting{
				ID:         doc.ID,
				Title:      title,
				Notes:      notes,
				Transcript: transcript,
				DateTime:   dt,
				Lists:      listMapping[doc.ID],
			}

			relPath, err := output.WriteMeeting(outputDir, m, overwrite, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  write error %s: %v\n", title, err)
				return
			}
			if relPath == "" {
				return
			}

			mu.Lock()
			manifest.Add(m.ID, relPath, m.DateTime)
			if err := manifest.Save(outputDir); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: save manifest: %v\n", err)
			}
			exported++
			mu.Unlock()

			fmt.Printf("  => %s\n", relPath)
		}()
	}

	wg.Wait()
	return exported
}

func exportViaRefreshToken(path string, outputDir string, createdAfter *string, overwrite bool, debug bool, exclude []string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read refresh token file: %w", err)
	}
	var rt refreshTokenFile
	if err := json.Unmarshal(b, &rt); err != nil {
		return 0, fmt.Errorf("parse refresh token file: %w", err)
	}
	if rt.RefreshToken == "" {
		return 0, fmt.Errorf("refresh_token is empty in %s", path)
	}
	if rt.ClientID == "" {
		return 0, fmt.Errorf("client_id is empty in %s", path)
	}

	creds := &api.Credentials{
		RefreshToken: rt.RefreshToken,
		ClientID:     rt.ClientID,
	}

	fmt.Println("Refreshing access token via WorkOS...")
	refreshed, err := api.RefreshCredentials(creds)
	if err != nil {
		return 0, fmt.Errorf("refresh: %w", err)
	}
	fmt.Println("  Token refreshed")

	return exportViaToken(refreshed.AccessToken, outputDir, createdAfter, overwrite, debug, exclude)
}

func isExcluded(lists []api.ListInfo, exclude []string) bool {
	for _, l := range lists {
		for _, ex := range exclude {
			if strings.EqualFold(l.Title, ex) {
				return true
			}
		}
	}
	return false
}

func exportViaAPI(outputDir string, createdAfter *string, overwrite bool, debug bool, exclude []string) (int, error) {
	creds, err := api.FindCredentials()
	if err != nil {
		return 0, fmt.Errorf("credentials: %w", err)
	}

	if creds.IsExpired() {
		fmt.Println("  Token expired, refreshing...")
		refreshed, refreshErr := api.RefreshCredentials(creds)
		if refreshErr != nil {
			fmt.Printf("  Refresh failed (%v), trying with expired token...\n", refreshErr)
		} else {
			creds = refreshed
			fmt.Println("  Token refreshed")
		}
	}

	docs, err := api.FetchAllDocumentsAfter(creds.AccessToken, createdAfter)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	fmt.Printf("  Found %d documents\n", len(docs))

	listMapping, _ := api.FetchDocumentLists(creds.AccessToken)
	if listMapping != nil {
		fmt.Printf("  Found %d folder(s)\n", len(listMapping))
	} else {
		fmt.Println("  No folder mapping available")
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].CreatedAt > docs[j].CreatedAt
	})

	manifest := output.LoadManifest(outputDir)
	return processDocs(docs, listMapping, manifest, creds.AccessToken, outputDir, overwrite, debug, exclude), nil
}
