// muveectl is the command-line interface for Muvee.
// It authenticates via OAuth (device-flow) and communicates with
// the Muvee API server using a long-lived API token stored locally.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ─── Config ──────────────────────────────────────────────────────────────────

type Config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muveectl", "config.json")
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return &Config{}, nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return &Config{}, nil
	}
	return &c, nil
}

func saveConfig(c *Config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ─── HTTP Client ─────────────────────────────────────────────────────────────

type client struct {
	cfg    *Config
	server string
	json   bool
}

func newClient(cfg *Config, serverOverride string, jsonMode bool) *client {
	s := cfg.Server
	if serverOverride != "" {
		s = serverOverride
	}
	s = strings.TrimRight(s, "/")
	return &client{cfg: cfg, server: s, json: jsonMode}
}

func (c *client) do(method, path string, body interface{}) (map[string]interface{}, error) {
	return c.doSlice(method, path, body)
}

func (c *client) doRaw(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, c.server+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errObj map[string]string
		if json.Unmarshal(raw, &errObj) == nil && errObj["error"] != "" {
			return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, errObj["error"])
		}
		return nil, fmt.Errorf("server error %d", resp.StatusCode)
	}
	return raw, nil
}

func (c *client) doSlice(method, path string, body interface{}) (map[string]interface{}, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, c.server+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errObj map[string]string
		if json.Unmarshal(raw, &errObj) == nil && errObj["error"] != "" {
			return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, errObj["error"])
		}
		return nil, fmt.Errorf("server error %d", resp.StatusCode)
	}

	// Try object first, then array
	var result map[string]interface{}
	if json.Unmarshal(raw, &result) == nil {
		return result, nil
	}
	// Array → wrap it
	var arr []interface{}
	if json.Unmarshal(raw, &arr) == nil {
		return map[string]interface{}{"items": arr}, nil
	}
	return nil, fmt.Errorf("unexpected response: %s", string(raw))
}

func (c *client) doArray(method, path string, body interface{}) ([]interface{}, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, c.server+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errObj map[string]string
		if json.Unmarshal(raw, &errObj) == nil && errObj["error"] != "" {
			return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, errObj["error"])
		}
		return nil, fmt.Errorf("server error %d", resp.StatusCode)
	}
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return arr, nil
}

// ─── Output helpers ───────────────────────────────────────────────────────────

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func str(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return fmt.Sprintf("%.0f", t)
		case bool:
			if t {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

func printTable(rows []interface{}, cols []string) {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		m, _ := r.(map[string]interface{})
		row := make([]string, len(cols))
		for j, c := range cols {
			row[j] = str(m, c)
			if len(row[j]) > widths[j] {
				widths[j] = len(row[j])
			}
		}
		data[i] = row
	}
	// Header
	for i, c := range cols {
		fmt.Printf("%-*s  ", widths[i], strings.ToUpper(c))
	}
	fmt.Println()
	for _, w := range widths {
		fmt.Print(strings.Repeat("─", w+2))
	}
	fmt.Println()
	for _, row := range data {
		for i, cell := range row {
			fmt.Printf("%-*s  ", widths[i], cell)
		}
		fmt.Println()
	}
}

// ─── Login ────────────────────────────────────────────────────────────────────

func cmdLogin(args []string, cfg *Config) error {
	serverFlag := ""
	for i, a := range args {
		if a == "--server" && i+1 < len(args) {
			serverFlag = args[i+1]
		}
	}
	if serverFlag != "" {
		cfg.Server = strings.TrimRight(serverFlag, "/")
	}
	if cfg.Server == "" {
		fmt.Print("Enter Muvee server URL (e.g. https://www.example.com): ")
		var s string
		fmt.Scanln(&s)
		cfg.Server = strings.TrimRight(s, "/")
	}

	// Start a local HTTP server to receive the token callback
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// Open the login page with the port so the user can pick a provider in the browser.
	loginURL := fmt.Sprintf("%s/login?port=%d", cfg.Server, port)
	fmt.Printf("Opening browser for authentication...\n%s\n\n", loginURL)
	openBrowser(loginURL)

	tokenCh := make(chan string, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("token")
			if token == "" {
				http.Error(w, "no token", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `<html><body style="font-family:monospace;background:#0f0f0f;color:#e0e0e0;padding:2rem">
<h2>✓ Authentication successful</h2><p>You can close this tab and return to the terminal.</p></body></html>`)
			tokenCh <- token
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	go func() {
		_ = srv.Serve(ln)
	}()

	select {
	case token := <-tokenCh:
		cfg.Token = token
		_ = srv.Shutdown(context.Background())
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println("Logged in successfully. Token saved to", configPath())
		return nil
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return fmt.Errorf("login timed out after 5 minutes")
	}
}


func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// ─── whoami ───────────────────────────────────────────────────────────────────

func cmdWhoami(c *client, jsonMode bool) error {
	result, err := c.do("GET", "/api/me", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Email: %s\nName:  %s\nRole:  %s\n", str(result, "email"), str(result, "name"), str(result, "role"))
	return nil
}

// ─── Projects ─────────────────────────────────────────────────────────────────

func cmdProjectsList(c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/projects", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No projects found.")
		return nil
	}
	printTable(items, []string{"id", "name", "domain_prefix", "git_branch"})
	return nil
}

func parseProjectFlags(args []string, p map[string]interface{}) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				p["name"] = args[i+1]
				i++
			}
		case "--git-url":
			if i+1 < len(args) {
				p["git_url"] = args[i+1]
				i++
			}
		case "--branch":
			if i+1 < len(args) {
				p["git_branch"] = args[i+1]
				i++
			}
		case "--domain":
			if i+1 < len(args) {
				p["domain_prefix"] = args[i+1]
				i++
			}
		case "--dockerfile":
			if i+1 < len(args) {
				p["dockerfile_path"] = args[i+1]
				i++
			}
		case "--auth-required":
			p["auth_required"] = true
		case "--no-auth":
			p["auth_required"] = false
		case "--auth-domains":
			if i+1 < len(args) {
				p["auth_allowed_domains"] = args[i+1]
				i++
			}
		case "--git-source":
			if i+1 < len(args) {
				p["git_source"] = args[i+1]
				i++
			}
		}
	}
}

func cmdProjectsCreate(args []string, c *client, jsonMode bool) error {
	p := map[string]interface{}{}
	parseProjectFlags(args, p)
	isHosted := p["git_source"] == "hosted"
	if p["name"] == nil {
		return fmt.Errorf("--name is required")
	}
	if !isHosted && p["git_url"] == nil {
		return fmt.Errorf("--git-url is required (or use --git-source hosted)")
	}
	result, err := c.do("POST", "/api/projects", p)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Created project %s (ID: %s)\n", str(result, "name"), str(result, "id"))
	if pushURL := str(result, "git_push_url"); pushURL != "" {
		fmt.Printf("Git Push URL:  %s\n", pushURL)
		fmt.Printf("\nPush your code:\n  git remote add muvee %s\n  git push muvee main\n", pushURL)
		fmt.Println("\nUse any username and your API token as the password.")
	}
	return nil
}

func cmdProjectsGet(id string, c *client, jsonMode bool) error {
	result, err := c.do("GET", "/api/projects/"+id, nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("ID:            %s\nName:          %s\nGit Source:    %s\nGit URL:       %s\nBranch:        %s\nDomain Prefix: %s\nDockerfile:    %s\n",
		str(result, "id"), str(result, "name"), str(result, "git_source"), str(result, "git_url"), str(result, "git_branch"),
		str(result, "domain_prefix"), str(result, "dockerfile_path"))
	if pushURL := str(result, "git_push_url"); pushURL != "" {
		fmt.Printf("Git Push URL:  %s\n", pushURL)
	}
	return nil
}

func cmdProjectsUpdate(id string, args []string, c *client, jsonMode bool) error {
	p := map[string]interface{}{}
	parseProjectFlags(args, p)
	result, err := c.do("PUT", "/api/projects/"+id, p)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Updated project %s\n", str(result, "name"))
	return nil
}

func cmdProjectsDelete(id string, c *client) error {
	_, err := c.do("DELETE", "/api/projects/"+id, nil)
	if err != nil {
		return err
	}
	fmt.Println("Deleted project", id)
	return nil
}

func cmdProjectsDeploy(id string, c *client, jsonMode bool) error {
	result, err := c.do("POST", "/api/projects/"+id+"/deploy", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Deployment triggered (ID: %s, status: %s)\n", str(result, "id"), str(result, "status"))
	return nil
}

func cmdProjectsDeployments(id string, c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/projects/"+id+"/deployments", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No deployments found.")
		return nil
	}
	printTable(items, []string{"id", "status", "commit_sha", "updated_at"})
	return nil
}

func cmdProjectsMetrics(id string, args []string, c *client, jsonMode bool) error {
	limit := "60"
	for i := 0; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			limit = args[i+1]
			i++
		}
	}
	items, err := c.doArray("GET", "/api/projects/"+id+"/metrics?limit="+limit, nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No metrics available. The container may not be running or metrics have not been collected yet.")
		return nil
	}
	// Pretty-print the latest sample at the top, then a compact table.
	latest, _ := items[0].(map[string]interface{})
	if latest != nil {
		fmt.Printf("Latest sample (collected_at: %s)\n", str(latest, "collected_at"))
		fmt.Printf("  CPU:        %s%%\n", str(latest, "cpu_percent"))
		memUsage := floatStr(latest, "mem_usage_bytes")
		memLimit := floatStr(latest, "mem_limit_bytes")
		fmt.Printf("  Memory:     %s / %s bytes\n", memUsage, memLimit)
		fmt.Printf("  Net Rx:     %s bytes  Tx: %s bytes\n",
			str(latest, "net_rx_bytes"), str(latest, "net_tx_bytes"))
		fmt.Printf("  Disk Read:  %s bytes  Write: %s bytes\n",
			str(latest, "block_read_bytes"), str(latest, "block_write_bytes"))
		fmt.Println()
	}
	if len(items) > 1 {
		fmt.Printf("History (%d samples):\n", len(items))
		printTable(items, []string{"collected_at", "cpu_percent", "mem_usage_bytes", "net_rx_bytes", "net_tx_bytes"})
	}
	return nil
}

func floatStr(m map[string]interface{}, key string) string {
	switch v := m[key].(type) {
	case float64:
		return fmt.Sprintf("%.0f", v)
	case string:
		return v
	}
	return ""
}

// ─── Port Forward ─────────────────────────────────────────────────────────────

func cmdPortForward(projectID string, args []string, c *client) error {
	localPort := "0" // 0 = auto-pick
	for i := 0; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			localPort = args[i+1]
			i++
		}
	}

	// Verify project exists and has a running deployment.
	proj, err := c.do("GET", "/api/projects/"+projectID, nil)
	if err != nil {
		return fmt.Errorf("fetch project: %w", err)
	}

	proxyBase := c.server + "/api/projects/" + projectID + "/proxy"

	ln, err := net.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	fmt.Printf("Forwarding 127.0.0.1:%d → %s (project: %s)\n",
		ln.Addr().(*net.TCPAddr).Port, str(proj, "domain_prefix")+"."+str(proj, "name"), projectID)
	fmt.Println("Press Ctrl+C to stop.")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetURL := proxyBase + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// Copy request headers.
		for k, vv := range r.Header {
			for _, v := range vv {
				proxyReq.Header.Add(k, v)
			}
		}
		// Set auth.
		proxyReq.Header.Set("Authorization", "Bearer "+c.cfg.Token)

		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers.
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	return http.Serve(ln, handler)
}

// ─── Workspace ────────────────────────────────────────────────────────────────

func cmdWorkspaceList(projectID string, args []string, c *client, jsonMode bool) error {
	path := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--path" && i+1 < len(args) {
			path = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "--") {
			path = args[i]
		}
	}
	url := "/api/projects/" + projectID + "/workspace"
	if path != "" {
		url += "?path=" + urlEscape(path)
	}
	items, err := c.doArray("GET", url, nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	for _, item := range items {
		m, _ := item.(map[string]interface{})
		if m == nil {
			continue
		}
		isDir, _ := m["is_dir"].(bool)
		name := str(m, "name")
		if isDir {
			name += "/"
		}
		size := str(m, "size")
		if isDir {
			size = "-"
		}
		fmt.Printf("%-40s %10s\n", name, size)
	}
	return nil
}

func cmdWorkspacePull(projectID string, args []string, c *client) error {
	remotePath := args[0]
	localPath := filepath.Base(remotePath)
	if len(args) >= 2 {
		localPath = args[1]
	}
	url := c.server + "/api/projects/" + projectID + "/workspace/download?path=" + urlEscape(remotePath)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	defer out.Close()
	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	fmt.Printf("Downloaded %s → %s (%d bytes)\n", remotePath, localPath, n)
	return nil
}

func cmdWorkspacePush(projectID string, args []string, c *client) error {
	localFile := args[0]
	remotePath := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--remote-path" && i+1 < len(args) {
			remotePath = args[i+1]
			i++
		}
	}
	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	// Build multipart body
	pr, pw := io.Pipe()
	mw := multipartWriter(pw)
	go func() {
		fw, werr := mw.CreateFormFile("file", info.Name())
		if werr == nil {
			_, _ = io.Copy(fw, f)
		}
		_ = mw.Close()
		_ = pw.Close()
	}()

	url := c.server + "/api/projects/" + projectID + "/workspace/upload"
	if remotePath != "" {
		url += "?path=" + urlEscape(remotePath)
	}
	req, err := http.NewRequest("POST", url, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	fmt.Printf("Uploaded %s → workspace:%s\n", localFile, remotePath)
	return nil
}

func cmdWorkspaceDelete(projectID, remotePath string, c *client) error {
	url := "/api/projects/" + projectID + "/workspace?path=" + urlEscape(remotePath)
	_, err := c.do("DELETE", url, nil)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted workspace:%s\n", remotePath)
	return nil
}

// ─── Hosted Git Repository Commands ──────────────────────────────────────────

func cmdRepoTree(projectID string, args []string, c *client, jsonMode bool) error {
	ref, path := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ref":
			if i+1 < len(args) {
				ref = args[i+1]
				i++
			}
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		}
	}
	params := "?"
	if ref != "" {
		params += "ref=" + urlEscape(ref) + "&"
	}
	if path != "" {
		params += "path=" + urlEscape(path) + "&"
	}
	result, err := c.doRaw("GET", "/api/projects/"+projectID+"/repo/tree"+params, nil)
	if err != nil {
		return err
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(result, &entries); err != nil {
		return err
	}
	if jsonMode {
		printJSON(entries)
		return nil
	}
	for _, e := range entries {
		t := str(e, "type")
		name := str(e, "name")
		if t == "tree" {
			fmt.Printf("  %s/\n", name)
		} else {
			fmt.Printf("  %s\n", name)
		}
	}
	return nil
}

func cmdRepoLog(projectID string, args []string, c *client, jsonMode bool) error {
	ref := ""
	limit := "20"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ref":
			if i+1 < len(args) {
				ref = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				limit = args[i+1]
				i++
			}
		}
	}
	params := "?limit=" + limit
	if ref != "" {
		params += "&ref=" + urlEscape(ref)
	}
	result, err := c.doRaw("GET", "/api/projects/"+projectID+"/repo/commits"+params, nil)
	if err != nil {
		return err
	}
	var commits []map[string]interface{}
	if err := json.Unmarshal(result, &commits); err != nil {
		return err
	}
	if jsonMode {
		printJSON(commits)
		return nil
	}
	for _, cm := range commits {
		sha := str(cm, "sha")
		if len(sha) > 8 {
			sha = sha[:8]
		}
		fmt.Printf("%s  %s  (%s, %s)\n", sha, str(cm, "message"), str(cm, "author"), str(cm, "date"))
	}
	return nil
}

func cmdRepoBranches(projectID string, c *client, jsonMode bool) error {
	result, err := c.doRaw("GET", "/api/projects/"+projectID+"/repo/branches", nil)
	if err != nil {
		return err
	}
	var branches []map[string]interface{}
	if err := json.Unmarshal(result, &branches); err != nil {
		return err
	}
	if jsonMode {
		printJSON(branches)
		return nil
	}
	for _, b := range branches {
		name := str(b, "name")
		if b["is_default"] == true {
			fmt.Printf("* %s\n", name)
		} else {
			fmt.Printf("  %s\n", name)
		}
	}
	return nil
}

func cmdRepoShow(projectID, refPath string, c *client) error {
	// refPath format: ref:path, e.g. "main:README.md"
	parts := strings.SplitN(refPath, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("expected format: <ref>:<path> (e.g. main:README.md)")
	}
	ref, path := parts[0], parts[1]
	params := "?ref=" + urlEscape(ref) + "&path=" + urlEscape(path)
	result, err := c.doRaw("GET", "/api/projects/"+projectID+"/repo/blob"+params, nil)
	if err != nil {
		return err
	}
	os.Stdout.Write(result)
	return nil
}

func multipartWriter(w io.Writer) *multipart.Writer {
	return multipart.NewWriter(w)
}

func urlEscape(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '_', b == '.', b == '~', b == '/':
			buf.WriteByte(b)
		default:
			fmt.Fprintf(&buf, "%%%02X", b)
		}
	}
	return buf.String()
}

// ─── Datasets ─────────────────────────────────────────────────────────────────

func cmdDatasetsList(c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/datasets", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No datasets found.")
		return nil
	}
	printTable(items, []string{"id", "name", "nfs_path", "version"})
	return nil
}

func cmdDatasetsCreate(args []string, c *client, jsonMode bool) error {
	d := map[string]interface{}{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				d["name"] = args[i+1]
				i++
			}
		case "--nfs-path":
			if i+1 < len(args) {
				d["nfs_path"] = args[i+1]
				i++
			}
		}
	}
	if d["name"] == nil || d["nfs_path"] == nil {
		return fmt.Errorf("--name and --nfs-path are required")
	}
	result, err := c.do("POST", "/api/datasets", d)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Created dataset %s (ID: %s)\n", str(result, "name"), str(result, "id"))
	return nil
}

func cmdDatasetsGet(id string, c *client, jsonMode bool) error {
	result, err := c.do("GET", "/api/datasets/"+id, nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("ID:       %s\nName:     %s\nNFS Path: %s\nVersion:  %s\nSize:     %s bytes\n",
		str(result, "id"), str(result, "name"), str(result, "nfs_path"),
		str(result, "version"), str(result, "size_bytes"))
	return nil
}

func cmdDatasetsScan(id string, c *client) error {
	_, err := c.do("POST", "/api/datasets/"+id+"/scan", nil)
	if err != nil {
		return err
	}
	fmt.Println("Scan triggered for dataset", id)
	return nil
}

func cmdDatasetsDelete(id string, c *client) error {
	_, err := c.do("DELETE", "/api/datasets/"+id, nil)
	if err != nil {
		return err
	}
	fmt.Println("Deleted dataset", id)
	return nil
}

// ─── Tokens ───────────────────────────────────────────────────────────────────

func cmdTokensList(projectID string, c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/projects/"+projectID+"/tokens", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No API tokens found for this project.")
		return nil
	}
	printTable(items, []string{"id", "name", "last_used_at", "created_at"})
	return nil
}

func cmdTokensCreate(projectID string, args []string, c *client, jsonMode bool) error {
	name := "Git Token"
	for i, a := range args {
		if a == "--name" && i+1 < len(args) {
			name = args[i+1]
		}
	}
	result, err := c.do("POST", "/api/projects/"+projectID+"/tokens", map[string]string{"name": name})
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Created token %q (ID: %s)\nToken: %s\n\nStore this value securely — it will not be shown again.\n",
		str(result, "name"), str(result, "id"), str(result, "token"))
	return nil
}

func cmdTokensDelete(projectID, tokenID string, c *client) error {
	_, err := c.do("DELETE", "/api/projects/"+projectID+"/tokens/"+tokenID, nil)
	if err != nil {
		return err
	}
	fmt.Println("Deleted token", tokenID)
	return nil
}

// ─── Secrets ──────────────────────────────────────────────────────────────────

func cmdSecretsList(c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/secrets", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No secrets found.")
		return nil
	}
	printTable(items, []string{"id", "name", "type", "created_at"})
	return nil
}

func cmdSecretsCreate(args []string, c *client, jsonMode bool) error {
	d := map[string]interface{}{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				d["name"] = args[i+1]
				i++
			}
		case "--type":
			if i+1 < len(args) {
				d["type"] = args[i+1]
				i++
			}
		case "--value":
			if i+1 < len(args) {
				d["value"] = args[i+1]
				i++
			}
		case "--value-file":
			if i+1 < len(args) {
				data, err := os.ReadFile(args[i+1])
				if err != nil {
					return fmt.Errorf("read value file: %w", err)
				}
				d["value"] = string(data)
				i++
			}
		}
	}
	if d["name"] == nil || d["type"] == nil || d["value"] == nil {
		return fmt.Errorf("--name, --type (password|ssh_key), and --value (or --value-file) are required")
	}
	result, err := c.do("POST", "/api/secrets", d)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Created secret %q (ID: %s, type: %s)\n", str(result, "name"), str(result, "id"), str(result, "type"))
	return nil
}

func cmdSecretsDelete(id string, c *client) error {
	_, err := c.do("DELETE", "/api/secrets/"+id, nil)
	if err != nil {
		return err
	}
	fmt.Println("Deleted secret", id)
	return nil
}

// ─── Project Secret Bindings ──────────────────────────────────────────────────

func cmdProjectSecretsList(projectID string, c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/projects/"+projectID+"/secrets", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No secrets bound to this project.")
		return nil
	}
	printTable(items, []string{"secret_id", "secret_name", "secret_type", "env_var_name", "use_for_git", "use_for_build", "build_secret_id"})
	return nil
}

func cmdProjectBindSecret(projectID string, args []string, c *client, jsonMode bool) error {
	secretID := ""
	envVar := ""
	useForGit := false
	useForBuild := false
	buildSecretID := ""
	gitUsername := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--secret-id":
			if i+1 < len(args) {
				secretID = args[i+1]
				i++
			}
		case "--env-var":
			if i+1 < len(args) {
				envVar = args[i+1]
				i++
			}
		case "--use-for-git":
			useForGit = true
		case "--use-for-build":
			useForBuild = true
		case "--build-secret-id":
			if i+1 < len(args) {
				buildSecretID = args[i+1]
				i++
			}
		case "--git-username":
			if i+1 < len(args) {
				gitUsername = args[i+1]
				i++
			}
		}
	}
	if secretID == "" {
		return fmt.Errorf("--secret-id is required")
	}
	// Default git_username for HTTPS PAT auth when --use-for-git is set and no username provided
	if useForGit && gitUsername == "" {
		gitUsername = "x-access-token"
	}

	// Default build_secret_id from secret name when --use-for-build is set.
	// Example: "GITHUB_TOKEN" -> "github_token".
	if useForBuild && strings.TrimSpace(buildSecretID) == "" {
		secrets, err := c.doArray("GET", "/api/secrets", nil)
		if err != nil {
			return fmt.Errorf("resolve default --build-secret-id: %w", err)
		}
		var secretName string
		for _, item := range secrets {
			m, _ := item.(map[string]interface{})
			if m != nil && str(m, "id") == secretID {
				secretName = str(m, "name")
				break
			}
		}
		if secretName == "" {
			return fmt.Errorf("cannot infer --build-secret-id: secret %s not found", secretID)
		}
		buildSecretID = normalizeBuildSecretID(secretName)
		if buildSecretID == "" {
			return fmt.Errorf("cannot infer --build-secret-id from secret name %q; please pass --build-secret-id explicitly", secretName)
		}
	}

	// Fetch current bindings, replace or add the target one, and PUT.
	current, err := c.doArray("GET", "/api/projects/"+projectID+"/secrets", nil)
	if err != nil {
		return err
	}
	bindings := []map[string]interface{}{}
	for _, item := range current {
		m, _ := item.(map[string]interface{})
		if m == nil || str(m, "secret_id") == secretID {
			continue
		}
		bindings = append(bindings, map[string]interface{}{
			"secret_id":       str(m, "secret_id"),
			"env_var_name":    str(m, "env_var_name"),
			"use_for_git":     m["use_for_git"],
			"use_for_build":   m["use_for_build"],
			"build_secret_id": str(m, "build_secret_id"),
			"git_username":    str(m, "git_username"),
		})
	}
	bindings = append(bindings, map[string]interface{}{
		"secret_id":       secretID,
		"env_var_name":    envVar,
		"use_for_git":     useForGit,
		"use_for_build":   useForBuild,
		"build_secret_id": buildSecretID,
		"git_username":    gitUsername,
	})
	result, err := c.do("PUT", "/api/projects/"+projectID+"/secrets", bindings)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Secret %s bound to project %s (env_var: %q, use_for_git: %v, use_for_build: %v, build_secret_id: %q, git_username: %q)\n",
		secretID, projectID, envVar, useForGit, useForBuild, buildSecretID, gitUsername)
	return nil
}

func normalizeBuildSecretID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	return out
}

func cmdProjectUnbindSecret(projectID, secretID string, c *client) error {
	current, err := c.doArray("GET", "/api/projects/"+projectID+"/secrets", nil)
	if err != nil {
		return err
	}
	bindings := []map[string]interface{}{}
	for _, item := range current {
		m, _ := item.(map[string]interface{})
		if m == nil || str(m, "secret_id") == secretID {
			continue
		}
		bindings = append(bindings, map[string]interface{}{
			"secret_id":       str(m, "secret_id"),
			"env_var_name":    str(m, "env_var_name"),
			"use_for_git":     m["use_for_git"],
			"use_for_build":   m["use_for_build"],
			"build_secret_id": str(m, "build_secret_id"),
			"git_username":    str(m, "git_username"),
		})
	}
	if _, err := c.do("PUT", "/api/projects/"+projectID+"/secrets", bindings); err != nil {
		return err
	}
	fmt.Printf("Secret %s unbound from project %s\n", secretID, projectID)
	return nil
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func usage() {
	fmt.Fprint(os.Stderr, `muveectl – Muvee command-line interface

Usage:
  muveectl <command> [subcommand] [flags]

Authentication:
  login [--server URL]          Authenticate via browser (Google OAuth)
  whoami                        Show the current authenticated user

Projects:
  projects list                 List projects
  projects create [flags]       Create a project
    --name NAME                 Project name (required)
    --git-url URL               Git repository URL (required)
    --branch BRANCH             Git branch (default: main)
    --domain PREFIX             Domain prefix (defaults to project name)
    --dockerfile PATH           Dockerfile path (default: Dockerfile)
    --auth-required             Enable Google OAuth protection via Traefik ForwardAuth
    --no-auth                   Disable Google OAuth protection
    --auth-domains DOMAINS      Comma-separated allowed email domains (e.g. example.com,corp.com)
  projects get ID               Get project details
  projects update ID [flags]    Update project configuration
    (same flags as create)
  projects deploy ID            Trigger a deployment
  projects deployments ID       List deployment history
  projects metrics ID [--limit N]
                                Show container resource metrics (CPU, mem, net, disk)
  projects port-forward ID [--port PORT]
                                Forward a local port to the project's running container
                                (auth is injected automatically using your CLI identity)
  projects delete ID            Delete a project

Datasets:
  datasets list                 List datasets
  datasets create [flags]       Create a dataset
    --name NAME                 Dataset name (required)
    --nfs-path PATH             NFS mount path (required)
  datasets get ID               Get dataset details
  datasets scan ID              Trigger an NFS scan
  datasets delete ID            Delete a dataset

API Tokens (project-scoped):
  tokens PROJECT_ID list                   List tokens for a project
  tokens PROJECT_ID create [--name NAME]   Create a project token
  tokens PROJECT_ID delete TOKEN_ID        Delete a project token

Secrets:
  secrets list                  List secrets (values are never returned)
  secrets create [flags]        Create a secret
    --name NAME                 Secret name (required)
    --type TYPE                 Type: password or ssh_key (required)
    --value VALUE               Secret value (required, or use --value-file)
    --value-file PATH           Read secret value from file (useful for SSH keys)
  secrets delete ID             Delete a secret

Project Secret Bindings:
  projects secrets PROJECT_ID             List secrets bound to a project
  projects bind-secret PROJECT_ID [flags] Attach a secret to a project
    --secret-id ID              Secret ID to bind (required)
    --env-var NAME              Environment variable name to inject (e.g. GITHUB_TOKEN)
    --use-for-git               Use this secret for git clone during build
    --use-for-build             Use this secret for docker buildx --secret during image build
    --build-secret-id ID        Secret ID exposed as /run/secrets/<ID> inside Dockerfile (optional; defaults from secret name)
    --git-username NAME         HTTPS git username (default: x-access-token for GitHub PATs)
                                Only relevant for password-type secrets with --use-for-git
  projects unbind-secret PROJECT_ID SECRET_ID
                                Detach a secret from a project

Global flags (available on all commands):
  --server URL                  Override the configured server URL
  --json                        Output raw JSON

Config is stored at: ~/.config/muveectl/config.json
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	// Extract global flags from any position
	serverOverride := ""
	jsonMode := false
	cleanArgs := []string{}
	for i, a := range os.Args[1:] {
		switch a {
		case "--json":
			jsonMode = true
		case "--server":
			if i+1 < len(os.Args[1:]) {
				serverOverride = os.Args[i+2]
			}
		default:
			if len(cleanArgs) > 0 && cleanArgs[len(cleanArgs)-1] == "--server" {
				// already captured
			} else {
				cleanArgs = append(cleanArgs, a)
			}
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		cfg = &Config{}
	}
	if serverOverride != "" {
		cfg.Server = serverOverride
	}

	cmd := cleanArgs[0]
	rest := cleanArgs[1:]

	if cmd == "login" {
		if err := cmdLogin(rest, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	}

	// All other commands need a configured server and token
	if cfg.Server == "" {
		fmt.Fprintln(os.Stderr, "No server configured. Run: muveectl login --server https://your-muvee-server")
		os.Exit(1)
	}
	if cfg.Token == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run: muveectl login")
		os.Exit(1)
	}

	c := newClient(cfg, serverOverride, jsonMode)

	var runErr error
	switch cmd {
	case "whoami":
		runErr = cmdWhoami(c, jsonMode)

	case "projects":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		sub := rest[0]
		subArgs := rest[1:]
		switch sub {
		case "list":
			runErr = cmdProjectsList(c, jsonMode)
		case "create":
			runErr = cmdProjectsCreate(subArgs, c, jsonMode)
		case "get":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects get <ID>")
				os.Exit(1)
			}
			runErr = cmdProjectsGet(subArgs[0], c, jsonMode)
		case "update":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects update <ID> [flags]")
				os.Exit(1)
			}
			runErr = cmdProjectsUpdate(subArgs[0], subArgs[1:], c, jsonMode)
		case "deploy":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects deploy <ID>")
				os.Exit(1)
			}
			runErr = cmdProjectsDeploy(subArgs[0], c, jsonMode)
		case "deployments":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects deployments <ID>")
				os.Exit(1)
			}
			runErr = cmdProjectsDeployments(subArgs[0], c, jsonMode)
		case "metrics":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects metrics <ID> [--limit N]")
				os.Exit(1)
			}
			runErr = cmdProjectsMetrics(subArgs[0], subArgs[1:], c, jsonMode)
		case "port-forward":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects port-forward <ID> [--port PORT]")
				os.Exit(1)
			}
			runErr = cmdPortForward(subArgs[0], subArgs[1:], c)
		case "delete":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects delete <ID>")
				os.Exit(1)
			}
			runErr = cmdProjectsDelete(subArgs[0], c)
		case "secrets":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects secrets <PROJECT_ID>")
				os.Exit(1)
			}
			runErr = cmdProjectSecretsList(subArgs[0], c, jsonMode)
		case "bind-secret":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects bind-secret <PROJECT_ID> --secret-id ID [--env-var NAME] [--use-for-git] [--git-username NAME] [--use-for-build --build-secret-id ID]")
				os.Exit(1)
			}
			runErr = cmdProjectBindSecret(subArgs[0], subArgs[1:], c, jsonMode)
		case "unbind-secret":
			if len(subArgs) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects unbind-secret <PROJECT_ID> <SECRET_ID>")
				os.Exit(1)
			}
			runErr = cmdProjectUnbindSecret(subArgs[0], subArgs[1], c)
		case "repo":
			if len(subArgs) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects repo <PROJECT_ID> <tree|log|branches|show> [args...]")
				os.Exit(1)
			}
			projectID := subArgs[0]
			repoCmd := subArgs[1]
			repoArgs := subArgs[2:]
			switch repoCmd {
			case "tree":
				runErr = cmdRepoTree(projectID, repoArgs, c, jsonMode)
			case "log":
				runErr = cmdRepoLog(projectID, repoArgs, c, jsonMode)
			case "branches":
				runErr = cmdRepoBranches(projectID, c, jsonMode)
			case "show":
				if len(repoArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: muveectl projects repo <PROJECT_ID> show <ref>:<path>")
					os.Exit(1)
				}
				runErr = cmdRepoShow(projectID, repoArgs[0], c)
			default:
				fmt.Fprintln(os.Stderr, "Unknown repo subcommand:", repoCmd)
				os.Exit(1)
			}
		case "workspace":
			if len(subArgs) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects workspace <PROJECT_ID> <ls|pull|push|rm> [args...]")
				os.Exit(1)
			}
			projectID := subArgs[0]
			wsCmd := subArgs[1]
			wsArgs := subArgs[2:]
			switch wsCmd {
			case "ls":
				runErr = cmdWorkspaceList(projectID, wsArgs, c, jsonMode)
			case "pull":
				if len(wsArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: muveectl projects workspace <PROJECT_ID> pull <REMOTE_PATH> [LOCAL_PATH]")
					os.Exit(1)
				}
				runErr = cmdWorkspacePull(projectID, wsArgs, c)
			case "push":
				if len(wsArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: muveectl projects workspace <PROJECT_ID> push <LOCAL_FILE> [--remote-path PATH]")
					os.Exit(1)
				}
				runErr = cmdWorkspacePush(projectID, wsArgs, c)
			case "rm":
				if len(wsArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: muveectl projects workspace <PROJECT_ID> rm <REMOTE_PATH>")
					os.Exit(1)
				}
				runErr = cmdWorkspaceDelete(projectID, wsArgs[0], c)
			default:
				fmt.Fprintln(os.Stderr, "Unknown workspace subcommand:", wsCmd)
				os.Exit(1)
			}
		default:
			fmt.Fprintln(os.Stderr, "Unknown projects subcommand:", sub)
			os.Exit(1)
		}

	case "datasets":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		sub := rest[0]
		subArgs := rest[1:]
		switch sub {
		case "list":
			runErr = cmdDatasetsList(c, jsonMode)
		case "create":
			runErr = cmdDatasetsCreate(subArgs, c, jsonMode)
		case "get":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl datasets get <ID>")
				os.Exit(1)
			}
			runErr = cmdDatasetsGet(subArgs[0], c, jsonMode)
		case "scan":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl datasets scan <ID>")
				os.Exit(1)
			}
			runErr = cmdDatasetsScan(subArgs[0], c)
		case "delete":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl datasets delete <ID>")
				os.Exit(1)
			}
			runErr = cmdDatasetsDelete(subArgs[0], c)
		default:
			fmt.Fprintln(os.Stderr, "Unknown datasets subcommand:", sub)
			os.Exit(1)
		}

	case "tokens":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muveectl tokens <PROJECT_ID> <list|create|delete> [args]")
			os.Exit(1)
		}
		projID := rest[0]
		sub := rest[1]
		subArgs := rest[2:]
		switch sub {
		case "list":
			runErr = cmdTokensList(projID, c, jsonMode)
		case "create":
			runErr = cmdTokensCreate(projID, subArgs, c, jsonMode)
		case "delete":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl tokens <PROJECT_ID> delete <TOKEN_ID>")
				os.Exit(1)
			}
			runErr = cmdTokensDelete(projID, subArgs[0], c)
		default:
			fmt.Fprintln(os.Stderr, "Unknown tokens subcommand:", sub)
			os.Exit(1)
		}

	case "secrets":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		sub := rest[0]
		subArgs := rest[1:]
		switch sub {
		case "list":
			runErr = cmdSecretsList(c, jsonMode)
		case "create":
			runErr = cmdSecretsCreate(subArgs, c, jsonMode)
		case "delete":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl secrets delete <ID>")
				os.Exit(1)
			}
			runErr = cmdSecretsDelete(subArgs[0], c)
		default:
			fmt.Fprintln(os.Stderr, "Unknown secrets subcommand:", sub)
			os.Exit(1)
		}

	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", cmd)
		usage()
		os.Exit(1)
	}

	if runErr != nil {
		fmt.Fprintln(os.Stderr, "Error:", runErr)
		os.Exit(1)
	}
}
