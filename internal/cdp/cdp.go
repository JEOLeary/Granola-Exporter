package cdp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

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

func (c *CDPExtractor) evaluate(wsURL, js string, awaitPromise bool) (interface{}, error) {
	params := map[string]interface{}{
		"expression":                js,
		"returnByValue":            true,
		"includeCommandLineAPI":    true,
		"allowUnsafeEvalBlockedByCSP": true,
		"userGesture":              true,
	}
	if awaitPromise {
		params["awaitPromise"] = true
	}
	resp, err := c.callCDP(wsURL, map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": params,
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

func (c *CDPExtractor) FetchToken() (string, error) {
	pids, _ := c.findGranolaProcesses()
	needsKill := false

	if len(pids) > 0 {
		targets, err := c.fetchTargets()
		if err == nil && len(targets) > 0 {
		} else {
			if err := c.killGranola(); err != nil {
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
		val, err := c.evaluate(target.WebSocketDebug, "(document.body.innerText || '').length", false)
		if err == nil {
			if l, ok := val.(float64); ok && l > 200 {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	val, err := c.evaluate(target.WebSocketDebug, `window.electron.ipcInvoke('get-session')`, true)
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

	return accessToken, nil
}
