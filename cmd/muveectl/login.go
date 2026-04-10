package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ─── Login ───────────────────────────────────────────────────────────────────

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate via browser (OAuth)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmdLogin(cfg)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func cmdLogin(cfg *Config) error {
	if serverOverride != "" {
		cfg.Server = strings.TrimRight(serverOverride, "/")
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
	fmt.Println("If you are on a remote server, open the URL above in your local browser.")
	fmt.Println("After authentication, paste the token shown on the page below.")
	fmt.Print("\nToken (or wait for automatic callback): ")
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

	// Also accept token from stdin for headless/remote server use.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			t := strings.TrimSpace(scanner.Text())
			if t != "" {
				tokenCh <- t
			}
		}
	}()

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
		fmt.Println("\nLogged in successfully. Token saved to", configPath())
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

// ─── Whoami ──────────────────────────────────────────────────────────────────

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the current authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/me", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Email: %s\nName:  %s\nRole:  %s\n", str(result, "email"), str(result, "name"), str(result, "role"))
		return nil
	},
}

// ─── Version ─────────────────────────────────────────────────────────────────

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}

// ─── Install Claude Skill ────────────────────────────────────────────────────

var installClaudeSkillCmd = &cobra.Command{
	Use:   "install-claude-skill",
	Short: "Install or update the Claude Code skill file",
	Long: fmt.Sprintf("Copies the embedded skill definition to %s",
		filepath.Join("~", ".claude", "skills", "muveectl", "SKILL.md")),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmdInstallClaudeSkill()
	},
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(installClaudeSkillCmd)
}
