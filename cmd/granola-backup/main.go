package main

import (
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
	"github.com/JEOLeary/granola-backup/internal/config"
	"github.com/JEOLeary/granola-backup/internal/mcp"
	"github.com/JEOLeary/granola-backup/internal/output"
	"github.com/JEOLeary/granola-backup/internal/sqlcipher"
)

func main() {
	configPath := config.FindPath()
	cfg := config.Load(configPath)
	if cfg == nil {
		cfg = &config.Config{}
	}

	outputDir := flag.String("output", config.StrVal(cfg.Output, "Granola.ai"), "Output directory for markdown files")
	tokenFile := flag.String("token-file", config.StrVal(cfg.TokenFile, ""), "MCP token file path (default: ./granola_token.json)")
	nodePath := flag.String("node-path", config.StrVal(cfg.NodePath, ""), "Path to Node.js 24+ executable")
	granolaPath := flag.String("granola-path",
		config.StrVal(cfg.GranolaPath, `C:\Users\JEOLeary\AppData\Local\Programs\granola\Granola.exe`),
		"Granola executable path")
	days := flag.Int("days", config.IntVal(cfg.Days, 365), "Number of days of history to fetch (only if -since or -since-last-export not used)")
	sinceStr := flag.String("since", config.StrVal(cfg.Since, ""), "Fetch meetings created after this date (YYYY-MM-DD or RFC3339). Takes priority over -days and -since-last-export")
	overwrite := flag.Bool("overwrite", config.BoolVal(cfg.Overwrite, false), "Overwrite existing files")
	debug := flag.Bool("debug", config.BoolVal(cfg.Debug, false), "Enable debug logging")
	sinceLastExport := flag.Bool("since-last-export", config.BoolVal(cfg.SinceLastExport, false), "Only fetch meetings since the latest exported file")
	exclude := flag.String("exclude", config.StrVal(cfg.Exclude, ""), "Comma-separated folder names to exclude (e.g. \"Personal,Brag Docs\")")
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

	createdAfter := computeCreatedAfter(outDir, *sinceLastExport, *days, *sinceStr)

	fmt.Println("=== Granola Backup ===")
	fmt.Println("Output:", outDir)
	if createdAfter != nil {
		fmt.Println("Since:", *createdAfter)
	} else {
		fmt.Println("Since: all available")
	}
	fmt.Println("Overwrite:", *overwrite)
	fmt.Println("Debug:", *debug)
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

	tokFile := *tokenFile
	if tokFile == "" {
		cwd, _ := os.Getwd()
		tokFile = filepath.Join(cwd, "granola_token.json")
	}

	mcpClient := mcp.NewMCPClient(tokFile)

	if mcpClient.HasToken() {
		fmt.Println("Trying MCP API...")
		exported, err := mcpClient.ExportAll(outDir, *overwrite)
		if err == nil {
			fmt.Printf("\nExported %d meetings via MCP.\n", exported)
			return
		}
		fmt.Printf("  MCP failed: %v\n", err)
		fmt.Println()
	}

	fmt.Println("Trying CDP token + Private API...")
	{
		cdpExt := cdp.NewCDPExtractor(*granolaPath)
		token, cdpErr := cdpExt.FetchToken()
		if cdpErr == nil {
			exported, apiErr := exportViaToken(token, outDir, createdAfter, *overwrite, *debug, excludeList)
			if apiErr == nil {
				fmt.Printf("\nExported %d meetings via CDP+API.\n", exported)
				return
			}
			fmt.Printf("  CDP+API failed: %v\n", apiErr)
		} else {
			fmt.Printf("  CDP token: %v\n", cdpErr)
		}
	}
	fmt.Println()

	fmt.Println("Trying file-based Private API...")
	exported, err := exportViaAPI(outDir, createdAfter, *overwrite, *debug, excludeList)
	if err == nil {
		fmt.Printf("\nExported %d meetings via Private API.\n", exported)
		return
	}
	fmt.Printf("  Private API failed: %v\n", err)
	fmt.Println()

	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".granola-backup")

	sqlcipherExt := sqlcipher.NewSQLCipherExtractor(*granolaPath, cacheDir, *nodePath)
	fmt.Println("Trying SQLCipher extraction...")
	exported, err = sqlcipherExt.ExportAll(outDir, *overwrite)
	if err == nil {
		fmt.Printf("\nExported %d meetings via SQLCipher.\n", exported)
		return
	}
	fmt.Printf("  SQLCipher failed: %v\n", err)
	fmt.Println()

	fmt.Println("Trying CDP extraction...")
	{
		cdpExt := cdp.NewCDPExtractor(*granolaPath)
		exported, err = cdpExt.ExtractAll(outDir, *overwrite)
		if err == nil {
			fmt.Printf("\nExported %d meetings via CDP.\n", exported)
			return
		}
		fmt.Printf("  CDP failed: %v\n", err)
	}

	fmt.Println("\nAll extraction methods failed.")
	fmt.Println("Try: run 'granola-backup' after launching Granola manually,")
	fmt.Println("  or authenticate with MCP (will prompt for browser auth on first run).")
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
