// muveectl is the command-line interface for Muvee.
// It authenticates via OAuth (device-flow) and communicates with
// the Muvee API server using a long-lived API token stored locally.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoveychen/muvee/internal/skill"
	"github.com/spf13/cobra"
)

var embeddedSkill = skill.Muveectl

var version = "dev"

// ─── Global state ────────────────────────────────────────────────────────────

var (
	serverOverride string
	jsonMode       bool
	cfg            *Config
	cl             *client
)

// ─── Root command ────────────────────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "muveectl",
	Short: "muveectl – Muvee command-line interface",
	Long:  "muveectl is the command-line interface for the Muvee self-hosted PaaS.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cfg, _ = loadConfig()
		cl = newClient(cfg, serverOverride, jsonMode)
		printNotices()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serverOverride, "server", "", "Override the configured server URL")
	rootCmd.PersistentFlags().BoolVar(&jsonMode, "json", false, "Output raw JSON")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func requireAuth() error {
	if cl.server == "" {
		return fmt.Errorf("not logged in. Run: muveectl login --server <URL>")
	}
	return nil
}

// ─── Config ──────────────────────────────────────────────────────────────────

// Config holds the CLI authentication state.
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

func floatStr(m map[string]interface{}, key string) string {
	switch v := m[key].(type) {
	case float64:
		return fmt.Sprintf("%.0f", v)
	case string:
		return v
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

func newMultipartWriter(w io.Writer) *multipart.Writer {
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

// ─── Notices ─────────────────────────────────────────────────────────────────

type updateCache struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

func updateCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muveectl", "update_check.json")
}

func claudeSkillPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills", "muveectl", "SKILL.md")
}

func parseSkillVersion(content string) string {
	for _, line := range strings.SplitN(content, "---", 3) {
		for _, l := range strings.Split(line, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "version:") {
				return strings.TrimSpace(strings.TrimPrefix(l, "version:"))
			}
		}
	}
	return ""
}

func checkSkillNotice() {
	installedData, err := os.ReadFile(claudeSkillPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Tip: Run `muveectl install-claude-skill` to add Claude Code skill support.")
		return
	}
	installedVersion := parseSkillVersion(string(installedData))
	embeddedVersion := parseSkillVersion(embeddedSkill)
	if embeddedVersion != "" && installedVersion != embeddedVersion {
		fmt.Fprintln(os.Stderr, "Notice: Claude skill is outdated. Run `muveectl install-claude-skill` to update.")
	}
}

func checkUpdateNotice() {
	cachePath := updateCachePath()
	var cache updateCache
	if data, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	// Print notice if cached result shows a newer version
	if cache.LatestVersion != "" && cache.LatestVersion != version && version != "dev" {
		fmt.Fprintf(os.Stderr, "Notice: New version available (%s → %s). Download: https://github.com/hoveychen/muvee/releases/latest\n", version, cache.LatestVersion)
	}
	// Refresh cache in background if stale (> 24h)
	if time.Since(cache.LastCheck) > 24*time.Hour {
		go func() {
			cl := &http.Client{Timeout: 5 * time.Second}
			resp, err := cl.Get("https://api.github.com/repos/hoveychen/muvee/releases/latest")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var result struct {
				TagName string `json:"tag_name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.TagName == "" {
				return
			}
			newCache := updateCache{LastCheck: time.Now(), LatestVersion: result.TagName}
			data, _ := json.MarshalIndent(newCache, "", "  ")
			_ = os.WriteFile(cachePath, data, 0600)
		}()
	}
}

func printNotices() {
	checkSkillNotice()
	checkUpdateNotice()
}

func cmdInstallClaudeSkill() error {
	path := claudeSkillPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(embeddedSkill), 0644); err != nil {
		return fmt.Errorf("writing skill file: %w", err)
	}
	fmt.Printf("Claude Code skill installed at %s\n", path)
	return nil
}
