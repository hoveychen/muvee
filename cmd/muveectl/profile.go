package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage muveectl credential profiles (kubectl-style multi-environment)",
	Long: `Profiles let you keep credentials for multiple Muvee servers
(e.g. dev / staging / prod) in one config file and switch between them.

The active profile is used unless overridden by:
  --profile <name>      (per-command flag)
  MUVEECTL_PROFILE      (environment variable)`,
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileAddCmd)
	profileCmd.AddCommand(profileRmCmd)
	profileCmd.AddCommand(profileCurrentCmd)
	profileCmd.AddCommand(profileShowCmd)

	profileAddCmd.Flags().String("server", "", "Server URL for the new profile (required)")
	profileAddCmd.Flags().String("token", "", "API token for the new profile (optional — run `muveectl login --profile <name>` later if omitted)")
	_ = profileAddCmd.MarkFlagRequired("server")
}

// sortedProfileNames returns profile names in deterministic order so list/show
// output is stable across runs.
func sortedProfileNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ─── List ────────────────────────────────────────────────────────────────────

var profileListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all profiles (active one marked with *)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(cfg.Profiles) == 0 {
			fmt.Println("No profiles. Run `muveectl login` to create one.")
			return nil
		}
		names := sortedProfileNames(cfg)
		if jsonMode {
			out := map[string]any{"current": cfg.CurrentProfile, "profiles": map[string]any{}}
			for _, n := range names {
				p := cfg.Profiles[n]
				out["profiles"].(map[string]any)[n] = map[string]any{
					"server":    p.Server,
					"has_token": p.Token != "",
				}
			}
			printJSON(out)
			return nil
		}
		rows := make([]interface{}, 0, len(names))
		for _, n := range names {
			p := cfg.Profiles[n]
			marker := " "
			if n == cfg.CurrentProfile {
				marker = "*"
			}
			rows = append(rows, map[string]interface{}{
				"current": marker,
				"name":    n,
				"server":  p.Server,
				"token":   maskedTokenState(p.Token),
			})
		}
		printTable(rows, []string{"current", "name", "server", "token"})
		return nil
	},
}

// maskedTokenState gives a yes/no answer without ever printing the token bytes.
func maskedTokenState(tok string) string {
	if tok == "" {
		return "(none)"
	}
	return "(set)"
}

// ─── Use ─────────────────────────────────────────────────────────────────────

var profileUseCmd = &cobra.Command{
	Use:   "use NAME",
	Short: "Switch the active profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile %q not found. Available: %s", name, strings.Join(sortedProfileNames(cfg), ", "))
		}
		cfg.CurrentProfile = name
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Switched to profile %q\n", name)
		return nil
	},
}

// ─── Add ─────────────────────────────────────────────────────────────────────

var profileAddCmd = &cobra.Command{
	Use:   "add NAME",
	Short: "Add a new profile (use `muveectl login --profile NAME` for OAuth-based token)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if name == "" {
			return fmt.Errorf("profile name cannot be empty")
		}
		if _, ok := cfg.Profiles[name]; ok {
			return fmt.Errorf("profile %q already exists. Edit with `muveectl login --profile %s` or remove first", name, name)
		}
		server, _ := cmd.Flags().GetString("server")
		token, _ := cmd.Flags().GetString("token")
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]Profile{}
		}
		cfg.Profiles[name] = Profile{Server: strings.TrimRight(server, "/"), Token: token}
		if cfg.CurrentProfile == "" {
			cfg.CurrentProfile = name
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Added profile %q (server=%s)\n", name, server)
		if token == "" {
			fmt.Printf("Tip: run `muveectl login --profile %s` to obtain a token.\n", name)
		}
		return nil
	},
}

// ─── Remove ──────────────────────────────────────────────────────────────────

var profileRmCmd = &cobra.Command{
	Use:     "rm NAME",
	Aliases: []string{"remove", "delete"},
	Short:   "Delete a profile from local config",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile %q not found", name)
		}
		delete(cfg.Profiles, name)
		if cfg.CurrentProfile == name {
			// Pick another profile to be active, or clear if none remain.
			cfg.CurrentProfile = ""
			for _, n := range sortedProfileNames(cfg) {
				cfg.CurrentProfile = n
				break
			}
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Deleted profile %q\n", name)
		if cfg.CurrentProfile != "" {
			fmt.Printf("Active profile is now %q\n", cfg.CurrentProfile)
		} else {
			fmt.Println("No profiles remain.")
		}
		return nil
	},
}

// ─── Current ─────────────────────────────────────────────────────────────────

var profileCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the active profile name",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.CurrentProfile == "" {
			return fmt.Errorf("no active profile")
		}
		fmt.Println(cfg.CurrentProfile)
		return nil
	},
}

// ─── Show ────────────────────────────────────────────────────────────────────

var profileShowCmd = &cobra.Command{
	Use:   "show [NAME]",
	Short: "Print server URL and token-presence for a profile (defaults to active)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := cfg.CurrentProfile
		if len(args) == 1 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("no profile specified and no active profile set")
		}
		p, ok := cfg.Profiles[name]
		if !ok {
			return fmt.Errorf("profile %q not found", name)
		}
		if jsonMode {
			printJSON(map[string]any{
				"name":      name,
				"server":    p.Server,
				"has_token": p.Token != "",
				"active":    name == cfg.CurrentProfile,
			})
			return nil
		}
		fmt.Printf("Name:   %s\nServer: %s\nToken:  %s\nActive: %v\n", name, p.Server, maskedTokenState(p.Token), name == cfg.CurrentProfile)
		return nil
	},
}
