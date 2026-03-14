// muveectl is the command-line interface for Muvee.
// It authenticates via Google OAuth (device-flow) and communicates with
// the Muvee API server using a long-lived API token stored locally.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	loginURL := fmt.Sprintf("%s/auth/cli/login?port=%d", cfg.Server, port)
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
		}
	}
}

func cmdProjectsCreate(args []string, c *client, jsonMode bool) error {
	p := map[string]interface{}{}
	parseProjectFlags(args, p)
	if p["name"] == nil || p["git_url"] == nil {
		return fmt.Errorf("--name and --git-url are required")
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
	fmt.Printf("ID:            %s\nName:          %s\nGit URL:       %s\nBranch:        %s\nDomain Prefix: %s\nDockerfile:    %s\n",
		str(result, "id"), str(result, "name"), str(result, "git_url"), str(result, "git_branch"),
		str(result, "domain_prefix"), str(result, "dockerfile_path"))
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

func cmdTokensList(c *client, jsonMode bool) error {
	items, err := c.doArray("GET", "/api/tokens", nil)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(items)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No API tokens found.")
		return nil
	}
	printTable(items, []string{"id", "name", "last_used_at", "created_at"})
	return nil
}

func cmdTokensCreate(args []string, c *client, jsonMode bool) error {
	name := "CLI Token"
	for i, a := range args {
		if a == "--name" && i+1 < len(args) {
			name = args[i+1]
		}
	}
	result, err := c.do("POST", "/api/tokens", map[string]string{"name": name})
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

func cmdTokensDelete(id string, c *client) error {
	_, err := c.do("DELETE", "/api/tokens/"+id, nil)
	if err != nil {
		return err
	}
	fmt.Println("Deleted token", id)
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
	printTable(items, []string{"secret_id", "secret_name", "secret_type", "env_var_name", "use_for_git"})
	return nil
}

func cmdProjectBindSecret(projectID string, args []string, c *client, jsonMode bool) error {
	secretID := ""
	envVar := ""
	useForGit := false
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
			"secret_id":    str(m, "secret_id"),
			"env_var_name": str(m, "env_var_name"),
			"use_for_git":  m["use_for_git"],
			"git_username": str(m, "git_username"),
		})
	}
	bindings = append(bindings, map[string]interface{}{
		"secret_id":    secretID,
		"env_var_name": envVar,
		"use_for_git":  useForGit,
		"git_username": gitUsername,
	})
	result, err := c.do("PUT", "/api/projects/"+projectID+"/secrets", bindings)
	if err != nil {
		return err
	}
	if jsonMode {
		printJSON(result)
		return nil
	}
	fmt.Printf("Secret %s bound to project %s (env_var: %q, use_for_git: %v, git_username: %q)\n",
		secretID, projectID, envVar, useForGit, gitUsername)
	return nil
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
			"secret_id":    str(m, "secret_id"),
			"env_var_name": str(m, "env_var_name"),
			"use_for_git":  m["use_for_git"],
			"git_username": str(m, "git_username"),
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
  projects delete ID            Delete a project

Datasets:
  datasets list                 List datasets
  datasets create [flags]       Create a dataset
    --name NAME                 Dataset name (required)
    --nfs-path PATH             NFS mount path (required)
  datasets get ID               Get dataset details
  datasets scan ID              Trigger an NFS scan
  datasets delete ID            Delete a dataset

API Tokens:
  tokens list                   List API tokens
  tokens create [--name NAME]   Create a new API token
  tokens delete ID              Delete an API token

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
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects bind-secret <PROJECT_ID> --secret-id ID [--env-var NAME] [--use-for-git]")
				os.Exit(1)
			}
			runErr = cmdProjectBindSecret(subArgs[0], subArgs[1:], c, jsonMode)
		case "unbind-secret":
			if len(subArgs) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl projects unbind-secret <PROJECT_ID> <SECRET_ID>")
				os.Exit(1)
			}
			runErr = cmdProjectUnbindSecret(subArgs[0], subArgs[1], c)
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
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		sub := rest[0]
		subArgs := rest[1:]
		switch sub {
		case "list":
			runErr = cmdTokensList(c, jsonMode)
		case "create":
			runErr = cmdTokensCreate(subArgs, c, jsonMode)
		case "delete":
			if len(subArgs) == 0 {
				fmt.Fprintln(os.Stderr, "Usage: muveectl tokens delete <ID>")
				os.Exit(1)
			}
			runErr = cmdTokensDelete(subArgs[0], c)
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
