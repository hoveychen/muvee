package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets (values are write-only and never returned)",
}

func init() {
	rootCmd.AddCommand(secretsCmd)

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsCreateCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)

	// secrets create flags
	secretsCreateCmd.Flags().String("name", "", "Secret name (required)")
	secretsCreateCmd.Flags().String("type", "", "Type: password, ssh_key, api_key, env_var, or registry (required)")
	secretsCreateCmd.Flags().String("value", "", "Secret value (for registry: the registry password/token)")
	secretsCreateCmd.Flags().String("value-file", "", "Read secret value from file (useful for SSH keys)")
	secretsCreateCmd.Flags().String("registry-addr", "", "Registry host for type=registry, e.g. ghcr.io (required for registry)")
	secretsCreateCmd.Flags().String("registry-username", "", "Registry login username for type=registry")
	secretsCreateCmd.MarkFlagRequired("name")
	secretsCreateCmd.MarkFlagRequired("type")
}

// ─── List ────────────────────────────────────────────────────────────────────

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secrets (values are never returned)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/secrets", nil)
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
		printTable(items, []string{"id", "name", "type", "value_preview", "registry_addr", "registry_username", "created_at"})
		return nil
	},
}

// ─── Create ──────────────────────────────────────────────────────────────────

var secretsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a secret",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		typ, _ := cmd.Flags().GetString("type")
		value, _ := cmd.Flags().GetString("value")
		valueFile, _ := cmd.Flags().GetString("value-file")
		registryAddr, _ := cmd.Flags().GetString("registry-addr")
		registryUsername, _ := cmd.Flags().GetString("registry-username")

		if valueFile != "" {
			data, err := os.ReadFile(valueFile)
			if err != nil {
				return fmt.Errorf("read value file: %w", err)
			}
			value = string(data)
		}
		if value == "" {
			return fmt.Errorf("--value or --value-file is required")
		}
		if typ == "registry" && registryAddr == "" {
			return fmt.Errorf("--registry-addr is required for --type registry")
		}

		d := map[string]interface{}{
			"name":  name,
			"type":  typ,
			"value": value,
		}
		if registryAddr != "" {
			d["registry_addr"] = registryAddr
		}
		if registryUsername != "" {
			d["registry_username"] = registryUsername
		}
		result, err := cl.do("POST", "/api/secrets", d)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		preview := str(result, "value_preview")
		if preview != "" {
			fmt.Printf("Created secret %q (ID: %s, type: %s, preview: %s)\n", str(result, "name"), str(result, "id"), str(result, "type"), preview)
		} else {
			fmt.Printf("Created secret %q (ID: %s, type: %s)\n", str(result, "name"), str(result, "id"), str(result, "type"))
		}
		return nil
	},
}

// ─── Delete ──────────────────────────────────────────────────────────────────

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a secret",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		_, err := cl.do("DELETE", "/api/secrets/"+args[0], nil)
		if err != nil {
			return err
		}
		fmt.Println("Deleted secret", args[0])
		return nil
	},
}
