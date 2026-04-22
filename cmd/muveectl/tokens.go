package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var tokensCmd = &cobra.Command{
	Use:   "tokens",
	Short: "Manage API tokens (project-scoped and personal)",
}

var tokensMeCmd = &cobra.Command{
	Use:   "me",
	Short: "Manage personal access tokens for your user account",
}

func init() {
	rootCmd.AddCommand(tokensCmd)

	tokensCmd.AddCommand(tokensListCmd)
	tokensCmd.AddCommand(tokensCreateCmd)
	tokensCmd.AddCommand(tokensDeleteCmd)

	tokensCmd.AddCommand(tokensMeCmd)
	tokensMeCmd.AddCommand(tokensMeListCmd)
	tokensMeCmd.AddCommand(tokensMeCreateCmd)
	tokensMeCmd.AddCommand(tokensMeDeleteCmd)

	// tokens create flags
	tokensCreateCmd.Flags().String("name", "Git Token", "Token name")

	// tokens me create flags
	tokensMeCreateCmd.Flags().String("name", "Personal Access Token", "Token name")
	tokensMeCreateCmd.Flags().String("expires", "", "Validity duration (e.g. 720h, 2160h). Empty = never expires.")
}

// ─── List ────────────────────────────────────────────────────────────────────

var tokensListCmd = &cobra.Command{
	Use:   "list PROJECT_ID",
	Short: "List tokens for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects/"+args[0]+"/tokens", nil)
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
	},
}

// ─── Create ──────────────────────────────────────────────────────────────────

var tokensCreateCmd = &cobra.Command{
	Use:   "create PROJECT_ID",
	Short: "Create a project token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		result, err := cl.do("POST", "/api/projects/"+args[0]+"/tokens", map[string]string{"name": name})
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
	},
}

// ─── Delete ──────────────────────────────────────────────────────────────────

var tokensDeleteCmd = &cobra.Command{
	Use:   "delete PROJECT_ID TOKEN_ID",
	Short: "Delete a project token",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		_, err := cl.do("DELETE", "/api/projects/"+args[0]+"/tokens/"+args[1], nil)
		if err != nil {
			return err
		}
		fmt.Println("Deleted token", args[1])
		return nil
	},
}

// ─── Personal Access Tokens (tokens me) ──────────────────────────────────────

var tokensMeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your personal access tokens",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/me/tokens", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No personal access tokens. Create one with: muveectl tokens me create --name <label>")
			return nil
		}
		printTable(items, []string{"id", "name", "expires_at", "last_used_at", "created_at"})
		return nil
	},
}

var tokensMeCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a personal access token (mvp_...) for your user",
	Long: `Create a personal access token that authenticates as you from automated
tools (AI agents, scripts). The token inherits your full account permissions;
treat it like a password. Use it with MUVEECTL_TOKEN, --token, or
Authorization: Bearer <token>.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		expires, _ := cmd.Flags().GetString("expires")
		body := map[string]string{"name": name}
		if expires != "" {
			body["expires_in"] = expires
		}
		result, err := cl.do("POST", "/api/me/tokens", body)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Created personal access token %q (ID: %s)\n", str(result, "name"), str(result, "id"))
		if exp := str(result, "expires_at"); exp != "" {
			fmt.Printf("Expires: %s\n", exp)
		} else {
			fmt.Println("Expires: never")
		}
		fmt.Printf("\nToken: %s\n\nStore this value securely — it will not be shown again.\n", str(result, "token"))
		return nil
	},
}

var tokensMeDeleteCmd = &cobra.Command{
	Use:   "delete TOKEN_ID",
	Short: "Revoke a personal access token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		if _, err := cl.do("DELETE", "/api/me/tokens/"+args[0], nil); err != nil {
			return err
		}
		fmt.Println("Revoked token", args[0])
		return nil
	},
}
