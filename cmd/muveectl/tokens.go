package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var tokensCmd = &cobra.Command{
	Use:   "tokens",
	Short: "Manage project API tokens",
}

func init() {
	rootCmd.AddCommand(tokensCmd)

	tokensCmd.AddCommand(tokensListCmd)
	tokensCmd.AddCommand(tokensCreateCmd)
	tokensCmd.AddCommand(tokensDeleteCmd)

	// tokens create flags
	tokensCreateCmd.Flags().String("name", "Git Token", "Token name")
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
