package cdp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JEOLeary/granola-backup/internal/output"
	"github.com/JEOLeary/granola-backup/internal/redact"
	"github.com/gorilla/websocket"
)

const debugPort = 9222

type CDPTarget struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	WebSocketDebug string `json:"webSocketDebuggerUrl"`
}

type CDPExtractor struct {
	granolaPath string
}

func NewCDPExtractor(granolaPath string) *CDPExtractor {
	if granolaPath == "" {
		granolaPath = `C:\Users\JEOLeary\AppData\Local\Programs\granola\Granola.exe`
	}
	return &CDPExtractor{granolaPath: granolaPath}
}

func (c *CDPExtractor) findGranolaProcesses() ([]int, error) {
	cmd := exec.Command("pwsh", "-NoProfile", "-Command",
		"Get-Process Granola -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			var pid int
			fmt.Sscanf(line, "%d", &pid)
			if pid > 0 {
				pids = append(pids, pid)
			}
		}
	}
	return pids, nil
}

func (c *CDPExtractor) killGranola() error {
	pids, _ := c.findGranolaProcesses()
	if len(pids) == 0 {
		return nil
	}
	exec.Command("taskkill", "/F", "/IM", "Granola.exe").Run()
	time.Sleep(3 * time.Second)
	pids, _ = c.findGranolaProcesses()
	if len(pids) > 0 {
		for _, pid := range pids {
			p, _ := os.FindProcess(pid)
			if p != nil {
				p.Kill()
			}
		}
	}
	time.Sleep(2 * time.Second)
	return nil
}

func (c *CDPExtractor) launchGranolaWithDebug() error {
	if err := c.killGranola(); err != nil {
		return fmt.Errorf("kill: %w", err)
	}

	cmd := exec.Command(c.granolaPath,
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	return nil
}

func (c *CDPExtractor) fetchTargets() ([]CDPTarget, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json", debugPort))
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var targets []CDPTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return targets, nil
}

func (c *CDPExtractor) findGranolaTarget(targets []CDPTarget) *CDPTarget {
	for _, t := range targets {
		if t.Type == "page" {
			return &t
		}
	}
	return nil
}

func (c *CDPExtractor) callCDP(wsURL string, req map[string]interface{}) (map[string]interface{}, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("ws write: %w", err)
	}

	var resp map[string]interface{}
	if err := conn.ReadJSON(&resp); err != nil {
		return nil, fmt.Errorf("ws read: %w", err)
	}

	if errVal, ok := resp["error"]; ok {
		return nil, fmt.Errorf("CDP error: %v", errVal)
	}

	return resp, nil
}

func (c *CDPExtractor) evaluateWithAsync(wsURL, js string) (interface{}, error) {
	resp, err := c.callCDP(wsURL, map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression":                js,
			"returnByValue":            true,
			"awaitPromise":             true,
			"includeCommandLineAPI":    true,
			"allowUnsafeEvalBlockedByCSP": true,
			"userGesture":              true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("callCDP: %w", err)
	}

	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		return nil, nil
	}
	if resultVal, ok := result["result"]; ok {
		if resMap, ok := resultVal.(map[string]interface{}); ok {
			return resMap["value"], nil
		}
	}

	return nil, fmt.Errorf("evaluate: unexpected result: %v", result)
}

func (c *CDPExtractor) evaluate(wsURL, js string) (interface{}, error) {
	resp, err := c.callCDP(wsURL, map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression":                js,
			"returnByValue":            true,
			"includeCommandLineAPI":    true,
			"allowUnsafeEvalBlockedByCSP": true,
			"userGesture":              true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("callCDP: %w", err)
	}

	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		return nil, nil
	}
	if resultVal, ok := result["result"]; ok {
		if resMap, ok := resultVal.(map[string]interface{}); ok {
			return resMap["value"], nil
		}
	}

	return nil, fmt.Errorf("evaluate: unexpected result: %v", result)
}

func (c *CDPExtractor) waitForContent(wsURL string) error {
	js := `
(function wait() {
    return new Promise(function(resolve) {
        var check = function() {
            var text = (document.body && document.body.innerText) || '';
            if (text.length > 500 && text.indexOf('Share') !== -1) {
                return resolve({ ready: true, length: text.length });
            }
            setTimeout(check, 1000);
        };
        setTimeout(check, 1000);
    });
})();
`
	resp, err := c.callCDP(wsURL, map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression":            js,
			"returnByValue":        true,
			"awaitPromise":         true,
			"userGesture":          true,
		},
	})
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	result, _ := resp["result"].(map[string]interface{})
	if resultResult, ok := result["result"].(map[string]interface{}); ok {
		if val, ok := resultResult["value"].(map[string]interface{}); ok {
			fmt.Printf("  Page ready: length=%v\n", val["length"])
		}
	}
	return nil
}

func (c *CDPExtractor) extractMeetings(wsURL string) ([]string, error) {
	js := `
(function() {
    var text = (document.body.innerText || '');
    var lines = text.split('\n').filter(function(l) { return l.trim(); });

    var titles = [];
    var seen = {};
    var skip = {'quick note':1,'search':1,'ctrl+k':1,'home':1,'shared with me':1,'chat':1,
                'spaces':1,'my notes':1,'personal':1,'add folder':1,'upgrade':1,'coming up':1,
                'me':1,'':1,'basic plan':1,'jason o\'leary':1};

    for (var i = 0; i < lines.length; i++) {
        var l = lines[i].trim();
        var lower = l.toLowerCase();

        if (skip[lower]) continue;
        if (lower === 'jason o\'leary') continue;
        if (/^(today|yesterday|mon|tue|wed|thu|fri|sat|sun)/i.test(lower)) continue;
        if (/^\d+:\d+ (am|pm)$/i.test(l)) continue;
        if (/^(january|february|march|april|may|june|july|august|september|october|november|december)$/i.test(lower)) continue;
        if (/^(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)$/i.test(lower)) continue;
        if (/^\d+$/.test(l)) continue;
        if (l.length < 5 || l.length > 200) continue;
        if (/^(check your|no upcoming|calendar|view plans|list recent)/i.test(l)) continue;

        if (!seen[l]) {
            seen[l] = true;
            titles.push(l);
        }
    }

    return { titles: titles, length: text.length, lines: lines };
})();
`
	val, err := c.evaluate(wsURL, js)
	if err != nil {
		return nil, fmt.Errorf("extract meetings: %w", err)
	}

	result, ok := val.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected extract result type: %T", val)
	}

	length, _ := result["length"].(float64)
	fmt.Printf("  Page text length: %.0f\n", length)

	titles, _ := result["titles"].([]interface{})
	var meetings []string
	for _, t := range titles {
		if s, ok := t.(string); ok {
			meetings = append(meetings, s)
		}
	}

	if len(meetings) == 0 {
		if lines, ok := result["lines"].([]interface{}); ok {
			fmt.Println("  Raw lines:")
			for _, line := range lines {
				if s, ok := line.(string); ok && len(s) > 0 {
					fmt.Println("    " + s)
				}
			}
		}
		return nil, fmt.Errorf("no meetings found in text")
	}

	return meetings, nil
}

func (c *CDPExtractor) searchHeap(wsURL string, knownTitles []string) ([]output.Meeting, error) {
	js := `
(function() {
    var results = [];
    var seen = new Set();

    function isMeetingLike(obj, path) {
        if (!obj || typeof obj !== 'object') return false;
        var keys = Object.keys(obj);
        var keyStr = keys.join(',').toLowerCase();
        if (keys.length >= 3 && keys.length < 50 &&
            (keyStr.indexOf('title') >= 0 || keyStr.indexOf('name') >= 0) &&
            (keyStr.indexOf('notes') >= 0 || keyStr.indexOf('transcript') >= 0 ||
             keyStr.indexOf('created') >= 0 || keyStr.indexOf('updated') >= 0 ||
             keyStr.indexOf('id') >= 0)) {
            return true;
        }
        return false;
    }

    function search(obj, path, depth) {
        if (depth > 5 || seen.size > 5000) return;
        if (!obj || typeof obj !== 'object') return;
        try { JSON.stringify(obj); } catch(e) { return; }

        if (isMeetingLike(obj, path)) {
            try {
                var str = JSON.stringify(obj);
                if (str.length > 100 && str.length < 200000) {
                    var id = str.slice(0, 200);
                    if (!seen.has(id)) {
                        seen.add(id);
                        results.push({ path: path, data: obj, size: str.length });
                    }
                }
            } catch(e) {}
            return;
        }

        var keys = Object.keys(obj);
        for (var i = 0; i < Math.min(keys.length, 100); i++) {
            try {
                var k = keys[i];
                var v = obj[k];
                if (v && typeof v === 'object') {
                    search(v, path + '.' + k, depth + 1);
                }
            } catch(e) {}
        }
    }

    var skipKeys = {'webkitStorageInfo':1,'webkitIndexedDB':1,'caches':1,'indexedDB':1,'chrome':1};

    var globalKeys = Object.getOwnPropertyNames(window);
    for (var i = 0; i < globalKeys.length; i++) {
        try {
            var k = globalKeys[i];
            if (skipKeys[k] || k === 'window' || k === 'self' || k === 'top' || k === 'parent' ||
                k === 'document' || k === 'location' || k === 'navigator' || k === 'history' ||
                k === 'screen' || k === 'frames' || k.startsWith('webkit')) continue;
            var v = window[k];
            if (v && typeof v === 'object') {
                search(v, 'window.' + k, 0);
            }
        } catch(e) {}
    }

    try {
        var rootEl = document.getElementById('root') || document.querySelector('#app');
        if (rootEl) {
            var reactKey = Object.keys(rootEl).find(function(k) { return k.startsWith('__reactFiber$'); });
            if (reactKey) {
                search(rootEl[reactKey], 'reactFiber', 0);
            }
        }
    } catch(e) {}

    return results;
})();
`
	val, err := c.evaluate(wsURL, js)
	if err != nil {
		return nil, fmt.Errorf("heap search: %w", err)
	}

	results, ok := val.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected heap search result: %T", val)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no data objects found in heap")
	}

	fmt.Printf("  Found %d data objects in heap\n", len(results))

	var meetings []output.Meeting
	for _, r := range results {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := rm["path"].(string)
		data, ok := rm["data"].(map[string]interface{})
		if !ok {
			continue
		}
		title, _ := data["title"].(string)
		if title == "" {
			continue
		}

		notes, _ := data["notes_markdown"].(string)
		if notes == "" {
			if n, ok := data["notes"]; ok {
				if s, ok := n.(string); ok {
					notes = s
				}
			}
		}
		transcript, _ := data["transcript"].(string)
		createdAt, _ := data["created_at"].(string)

		var dt time.Time
		if createdAt != "" {
			dt, _ = time.Parse(time.RFC3339, createdAt)
		}

		fmt.Printf("  Heap: %s -> %s\n", path, title)
		meetings = append(meetings, output.Meeting{
			Title:      title,
			Notes:      notes,
			Transcript: transcript,
			DateTime:   dt,
		})
	}

	if len(meetings) == 0 {
		return nil, fmt.Errorf("no valid meetings found in heap data")
	}

	return meetings, nil
}

func (c *CDPExtractor) clickThroughMeetings(wsURL string, meetings []string, outputDir string, overwrite bool) (int, error) {
	exported := 0
	navigateHome := func() {
		c.evaluate(wsURL, `(function(){
    var links = document.querySelectorAll('a, button, [role=button], nav a');
    for (var i = 0; i < links.length; i++) {
        var t = (links[i].textContent || '').trim().toLowerCase();
        if (t === 'home') { links[i].click(); return 'home'; }
    }
    return 'no home found';
})()`)
		time.Sleep(2 * time.Second)
	}

	navigateHome()

	for i, title := range meetings {
		if strings.HasPrefix(title, "Voya") || title == "Jason HQ" || title == "Personal" ||
			title == "My notes" || strings.HasPrefix(title, "View") || strings.HasPrefix(title, "List") ||
			title == "Add folder" || strings.Contains(title, "Upgrade") {
			continue
		}

		fmt.Printf("  [%d/%d] %s\n", i+1, len(meetings), title)

		clickResult := "not found"
		for attempt := 0; attempt < 3 && clickResult == "not found"; attempt++ {
			clickJS := fmt.Sprintf(`(function(){
    var container = document.querySelector('main') || document.querySelector('[class*=content]') || document.body;
    var els = container.querySelectorAll('a, button, [role=button], [role=listitem], [class*="item"], [class*="card"]');
    var target = null;
    for (var i = 0; i < els.length; i++) {
        var t = (els[i].textContent || '').trim();
        if (t.indexOf('%s') !== -1) { target = els[i]; break; }
    }
    if (target) {
        target.scrollIntoView({behavior:'instant', block:'center'});
        target.click();
        return 'clicked';
    }
    return 'not found';
})().toString()`, title)

			var raw interface{}
			var evErr error
			raw, evErr = c.evaluate(wsURL, clickJS)
			if evErr != nil {
				fmt.Printf("    click error: %v\n", evErr)
				clickResult = "not found"
				navigateHome()
				break
			}
			clickResult, _ = raw.(string)
			if clickResult == "" {
				clickResult = "clicked"
			}
			fmt.Printf("    %v\n", clickResult)
			if clickResult != "not found" {
				break
			}
			fmt.Printf("    scrolling for retry...\n")
			c.evaluate(wsURL, `(function(){
    var containers = document.querySelectorAll('[style*="overflow"], [class*="scroll"], [class*="virtual"], main');
    for (var i = 0; i < containers.length; i++) {
        if (containers[i].scrollHeight > containers[i].clientHeight) {
            containers[i].scrollTop += containers[i].clientHeight * 0.8;
        }
    }
    return 'scrolled';
})()`)
			time.Sleep(2 * time.Second)
		}

		time.Sleep(3 * time.Second)

		detailJS := `
(function() {
    var text = document.body.innerText || '';
    var lines = text.split('\n');

    var addToFolderIdx = -1;
    for (var i = lines.length - 1; i >= 0; i--) {
        if (lines[i].trim() === 'Add to folder') { addToFolderIdx = i; break; }
    }

    var notes = '';
    if (addToFolderIdx >= 0) {
        for (var i = addToFolderIdx + 1; i < lines.length; i++) {
            var l = lines[i].trim();
            if (/^(today|yesterday|mon|tue|wed|thu|fri|sat|sun)/i.test(l) && !/^\d+:\d+/.test(l) && l.length < 15) break;
            if (/^[a-z]+ \d+$|^\d+ [a-z]+$|^[a-z]+ \d+,? \d{4}$/i.test(l)) break;
            if (l === '' || l === 'Coming up' || l.indexOf('No upcoming') === 0 || l.indexOf('Check your') === 0) continue;
            notes += l + '\n';
        }
    }

    var dateStr = '';
    for (var i = 0; i < lines.length; i++) {
        var l = lines[i].trim();
        if (l === 'Jason O\u2019Leary' || l === "Jason O'Leary" || l === 'Jason OLeary') {
            if (i + 1 < lines.length) {
                dateStr = lines[i + 1].trim();
            }
            break;
        }
    }

    if (notes === '') {
        var meIdx = -1;
        for (var i = 0; i < lines.length; i++) {
            var l = lines[i].trim();
            if (l === 'Jason O\u2019Leary' || l === "Jason O'Leary" || l === 'Jason OLeary') {
                var dateLine = (i + 1 < lines.length) ? lines[i + 1].trim() : '';
                if (dateLine.length > 0 && dateLine.length < 30) {
                    var speakerIdx = -1;
                    for (var j = i + 2; j < Math.min(i + 10, lines.length); j++) {
                        if (lines[j].trim() === 'Me') { speakerIdx = j; break; }
                    }
                    if (speakerIdx >= 0) { meIdx = speakerIdx; break; }
                }
            }
        }
        if (meIdx >= 0) {
            for (var i = meIdx + 1; i < Math.min(meIdx + 500, lines.length); i++) {
                var l = lines[i].trim();
                if (/^(today|yesterday|mon|tue|wed|thu|fri|sat|sun)/i.test(l) && !/^\d+:\d+/.test(l) && l.length < 15) break;
                if (/^[a-z]+ \d+$|^\d+ [a-z]+$|^[a-z]+ \d+,? \d{4}$/i.test(l)) break;
                if (l === '' || l === 'Coming up' || l.indexOf('No upcoming') === 0 || l.indexOf('Check your') === 0) continue;
                notes += l + '\n';
            }
        }
    }

    return {
        text: text.slice(0, 100000),
        notes: notes.trim(),
        date: dateStr
    };
})();
`

		detail, err := c.evaluate(wsURL, detailJS)
		if err != nil {
			fmt.Printf("    detail error: %v\n", err)
			navigateHome()
			continue
		}

		var meeting *output.Meeting
		if detailMap, ok := detail.(map[string]interface{}); ok {
			notes, _ := detailMap["notes"].(string)
			dateRaw, _ := detailMap["date"].(string)
			meeting = &output.Meeting{
				Title:    title,
				Notes:    notes,
				DateTime: parseDateStr(dateRaw),
			}
		}

		if meeting == nil {
			meeting = &output.Meeting{Title: title, DateTime: time.Now()}
		}

		fname, err := output.WriteMeeting(outputDir, *meeting, overwrite, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    write error: %v\n", err)
			navigateHome()
			continue
		}
		if fname != "" {
			fmt.Println("    => " + fname)
			exported++
		}

		navigateHome()
		time.Sleep(2 * time.Second)
	}

	return exported, nil
}

func (c *CDPExtractor) promptCloseGranola() error {
	fmt.Println("  Granola is running but not on the debug port.")
	fmt.Println("  Please close Granola manually so we can restart it with the debug port...")
	for i := 0; i < 10; i++ {
		pids, _ := c.findGranolaProcesses()
		if len(pids) == 0 {
			fmt.Println("  Granola closed. Continuing...")
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	pids, _ := c.findGranolaProcesses()
	if len(pids) > 0 {
		return fmt.Errorf("Granola did not close within 10 seconds")
	}
	return nil
}

func (c *CDPExtractor) FetchToken() (string, error) {
	pids, _ := c.findGranolaProcesses()
	needsKill := false

	if len(pids) > 0 {
		targets, err := c.fetchTargets()
		if err == nil && len(targets) > 0 {
		} else {
			if err := c.promptCloseGranola(); err != nil {
				return "", err
			}
			if err := c.launchGranolaWithDebug(); err != nil {
				return "", fmt.Errorf("launch: %w", err)
			}
			needsKill = true
		}
	} else {
		if err := c.launchGranolaWithDebug(); err != nil {
			return "", fmt.Errorf("launch: %w", err)
		}
		needsKill = true
	}

	if needsKill {
		defer c.killGranola()
	}

	var targets []CDPTarget
	var err error
	for i := 0; i < 30; i++ {
		targets, err = c.fetchTargets()
		if err == nil && len(targets) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || len(targets) == 0 {
		return "", fmt.Errorf("could not connect to debug targets: %w", err)
	}

	target := c.findGranolaTarget(targets)
	if target == nil {
		return "", fmt.Errorf("no page target found")
	}

	for i := 0; i < 30; i++ {
		val, err := c.evaluate(target.WebSocketDebug, "(document.body.innerText || '').length")
		if err == nil {
			if l, ok := val.(float64); ok && l > 200 {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	val, err := c.evaluateWithAsync(target.WebSocketDebug, `window.electron.ipcInvoke('get-session')`)
	if err != nil {
		return "", fmt.Errorf("get-session: %w", err)
	}

	session, ok := val.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected session response: %T", val)
	}

	accessToken, _ := session["access_token"].(string)
	if accessToken == "" {
		return "", fmt.Errorf("no access_token in session response")
	}

	var userName, userEmail string
	switch u := session["user"].(type) {
	case map[string]interface{}:
		userName, _ = u["name"].(string)
		userEmail, _ = u["email"].(string)
	case string:
		var userObj map[string]interface{}
		if err := json.Unmarshal([]byte(u), &userObj); err == nil {
			userName, _ = userObj["name"].(string)
			userEmail, _ = userObj["email"].(string)
		}
	}
	if userEmail != "" {
		fmt.Printf("  CDP token: user=%s email=%s\n", userName, userEmail)
	}

	if tokenDir := os.Getenv("TEMP"); tokenDir != "" {
		tokenPath := filepath.Join(tokenDir, "granola_token_live.txt")
		os.WriteFile(tokenPath, []byte(accessToken), 0600)
	}

	return accessToken, nil
}

func (c *CDPExtractor) ExtractAll(outputDir string, overwrite bool) (int, error) {
	fmt.Println("Starting CDP extraction...")

	pids, _ := c.findGranolaProcesses()
	needsKill := false

	if len(pids) > 0 {
		targets, err := c.fetchTargets()
		if err == nil && len(targets) > 0 {
		} else {
			if err := c.promptCloseGranola(); err != nil {
				return 0, err
			}
			if err := c.launchGranolaWithDebug(); err != nil {
				return 0, fmt.Errorf("launch: %w", err)
			}
			needsKill = true
		}
	} else {
		if err := c.launchGranolaWithDebug(); err != nil {
			return 0, fmt.Errorf("launch: %w", err)
		}
		needsKill = true
	}

	if needsKill {
		defer c.killGranola()
	}

	fmt.Println("  Granola connected on port", debugPort)

	var targets []CDPTarget
	var err error
	for i := 0; i < 20; i++ {
		targets, err = c.fetchTargets()
		if err == nil && len(targets) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || len(targets) == 0 {
		return 0, fmt.Errorf("could not connect to debug targets: %w", err)
	}

	target := c.findGranolaTarget(targets)
	if target == nil {
		return 0, fmt.Errorf("no page target found (%d targets)", len(targets))
	}

	fmt.Println("  Connected to: " + target.Title)

	fmt.Println("  Waiting for page content...")
	for i := 0; i < 30; i++ {
		val, err := c.evaluate(target.WebSocketDebug, "(document.body.innerText || '').length")
		if err == nil {
			if l, ok := val.(float64); ok && l > 200 {
				fmt.Printf("  Page loaded: %d chars\n", int(l))
				break
			}
		}
		if i == 29 {
			return 0, fmt.Errorf("page did not load within 60 seconds")
		}
		time.Sleep(2 * time.Second)
	}

	fmt.Println("  Extracting meeting list...")
	meetings, err := c.extractMeetings(target.WebSocketDebug)
	if err != nil {
		return 0, fmt.Errorf("extract meetings: %w", err)
	}

	var extraTitles []string
	for i, title := range meetings {
		if strings.HasPrefix(title, "Voya") || title == "Jason HQ" || title == "Personal" ||
			title == "My notes" || strings.HasPrefix(title, "View") || strings.HasPrefix(title, "List") ||
			title == "Add folder" || strings.Contains(title, "Upgrade") {
			continue
		}
		extraTitles = append(extraTitles, meetings[i])
	}
	meetings = extraTitles

	fmt.Printf("  Found %d meetings:\n", len(meetings))
	for _, m := range meetings {
		fmt.Println("    " + m)
	}

	if raw, err := c.evaluate(target.WebSocketDebug, `(function(){
    try {
        var items = [];
        for (var i = 0; i < localStorage.length; i++) {
            var key = localStorage.key(i);
            var val = localStorage.getItem(key);
            if (val && val.length > 50 && (val.indexOf('eyJ') >= 0 || val.indexOf('access_token') >= 0)) {
                return key + '=' + val.substring(0, 200);
            }
            if (val) items.push(key + ':' + val.substring(0, 40));
        }
        return JSON.stringify(items);
    } catch(e) { return 'err: ' + e.message; }
})()`); err == nil {
		if s, ok := raw.(string); ok && s != "" {
			if len(s) > 100 {
				fmt.Printf("  localStorage token: %s\n", redact.String(s[:200]))
			}
		}
	}

	if raw, err := c.evaluate(target.WebSocketDebug, `(function(){
    try {
        var checks = ['__GRANOLA__', '__INITIAL_STATE__', '__SUPABASE__', '__AUTH__',
            'window.__SUPABASE_AUTH__', 'window.__STORE__'];
        for (var c = 0; c < checks.length; c++) {
            var parts = checks[c].split('.');
            var obj = window;
            for (var p = 0; p < parts.length; p++) {
                if (!obj || !obj[parts[p]]) { obj = null; break; }
                obj = obj[parts[p]];
            }
            if (obj) {
                var str = JSON.stringify(obj);
                if (str.indexOf('eyJ') >= 0) return str.substring(0, 500);
            }
        }

        for (var i = 0; i < localStorage.length; i++) {
            var key = localStorage.key(i);
            var val = localStorage.getItem(key);
            if (val && val.indexOf('eyJ') >= 0) return 'ls:' + key + '=' + val.substring(0, 300);
        }

        var tokenKeys = ['accessToken', 'access_token', 'token', 'jwt', 'supabaseToken', 'granolaToken',
            'workosToken', 'authToken', 'sessionToken', 'bearerToken'];
        for (var k = 0; k < tokenKeys.length; k++) {
            var key = tokenKeys[k];
            if (window[key]) {
                var v = (typeof window[key] === 'string') ? window[key] : JSON.stringify(window[key]);
                if (v.indexOf('eyJ') >= 0) return 'window.' + key + '=' + v.substring(0, 300);
            }
        }

        return '';
    } catch(e) { return 'err: ' + e.message; }
})()`); err == nil {
		if s, ok := raw.(string); ok && s != "" && !strings.HasPrefix(s, "err:") {
			fmt.Printf("  Found auth data: %s\n", redact.String(s))
		}
	}

	fmt.Println("  Searching renderer memory for note data...")
	meetingData, err := c.searchHeap(target.WebSocketDebug, meetings)
	if err != nil {
		fmt.Printf("  Heap search: %v\n", err)
		fmt.Println("  Falling back to click-through extraction...")
		return c.clickThroughMeetings(target.WebSocketDebug, meetings, outputDir, overwrite)
	}

	exported := 0
	for _, m := range meetingData {
		fname, err := output.WriteMeeting(outputDir, m, overwrite, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    write error: %v\n", err)
			continue
		}
		if fname != "" || overwrite {
			fmt.Println("  => " + fname)
			exported++
		}
	}

	return exported, nil
}

func parseDateStr(s string) time.Time {
	now := time.Now()
	s = strings.TrimSpace(s)
	if s == "" {
		return now
	}

	lower := strings.ToLower(s)
	switch lower {
	case "today":
		return now
	case "yesterday":
		return now.AddDate(0, 0, -1)
	}

	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
		"saturday": time.Saturday,
	}
	if wd, ok := weekdays[lower]; ok {
		daysBack := int(now.Weekday() - wd)
		if daysBack <= 0 {
			daysBack += 7
		}
		return now.AddDate(0, 0, -daysBack)
	}

	formats := []string{
		"Jan 2, 2006",
		"January 2, 2006",
		"Jan 2",
		"January 2",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			if t.Year() == 0 {
				t = time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
			}
			if t.After(now) {
				t = t.AddDate(-1, 0, 0)
			}
			return t
		}
	}

	return now
}
