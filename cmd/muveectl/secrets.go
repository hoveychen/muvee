package main

import (
	"fmt"
	"os"
	"strings"

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
	secretsCreateCmd.Flags().String("type", "", "Type: password or ssh_key (required)")
	secretsCreateCmd.Flags().String("value", "", "Secret value")
	secretsCreateCmd.Flags().String("value-file", "", "Read secret value from file (useful for SSH keys)")
	secretsCreateCmd.MarkFlagRequired("name")
	secretsCreateCmd.MarkFlagRequired("type")

	// Project secret bindings
	projectsCmd.AddCommand(projectsSecretsCmd)
	projectsCmd.AddCommand(projectsBindSecretCmd)
	projectsCmd.AddCommand(projectsUnbindSecretCmd)

	// bind-secret flags
	projectsBindSecretCmd.Flags().String("secret-id", "", "Secret ID to bind (required)")
	projectsBindSecretCmd.Flags().String("env-var", "", "Environment variable name to inject")
	projectsBindSecretCmd.Flags().Bool("use-for-git", false, "Use this secret for git clone during build")
	projectsBindSecretCmd.Flags().Bool("use-for-build", false, "Use this secret for docker buildx --secret during image build")
	projectsBindSecretCmd.Flags().String("build-secret-id", "", "Secret ID exposed as /run/secrets/<ID> inside Dockerfile")
	projectsBindSecretCmd.Flags().String("git-username", "", "HTTPS git username (default: x-access-token for GitHub PATs)")
	projectsBindSecretCmd.MarkFlagRequired("secret-id")
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
		printTable(items, []string{"id", "name", "type", "created_at"})
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

		d := map[string]interface{}{
			"name":  name,
			"type":  typ,
			"value": value,
		}
		result, err := cl.do("POST", "/api/secrets", d)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Created secret %q (ID: %s, type: %s)\n", str(result, "name"), str(result, "id"), str(result, "type"))
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

// ─── Project Secret Bindings ─────────────────────────────────────────────────

var projectsSecretsCmd = &cobra.Command{
	Use:   "secrets PROJECT_ID",
	Short: "List secrets bound to a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects/"+args[0]+"/secrets", nil)
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
	},
}

var projectsBindSecretCmd = &cobra.Command{
	Use:   "bind-secret PROJECT_ID",
	Short: "Attach a secret to a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		secretID, _ := cmd.Flags().GetString("secret-id")
		envVar, _ := cmd.Flags().GetString("env-var")
		useForGit, _ := cmd.Flags().GetBool("use-for-git")
		useForBuild, _ := cmd.Flags().GetBool("use-for-build")
		buildSecretID, _ := cmd.Flags().GetString("build-secret-id")
		gitUsername, _ := cmd.Flags().GetString("git-username")

		// Default git_username for HTTPS PAT auth when --use-for-git is set and no username provided
		if useForGit && gitUsername == "" {
			gitUsername = "x-access-token"
		}

		// Default build_secret_id from secret name when --use-for-build is set.
		if useForBuild && strings.TrimSpace(buildSecretID) == "" {
			secrets, err := cl.doArray("GET", "/api/secrets", nil)
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
		current, err := cl.doArray("GET", "/api/projects/"+projectID+"/secrets", nil)
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
		result, err := cl.do("PUT", "/api/projects/"+projectID+"/secrets", bindings)
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
	},
}

var projectsUnbindSecretCmd = &cobra.Command{
	Use:   "unbind-secret PROJECT_ID SECRET_ID",
	Short: "Detach a secret from a project",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		secretID := args[1]
		current, err := cl.doArray("GET", "/api/projects/"+projectID+"/secrets", nil)
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
		if _, err := cl.do("PUT", "/api/projects/"+projectID+"/secrets", bindings); err != nil {
			return err
		}
		fmt.Printf("Secret %s unbound from project %s\n", secretID, projectID)
		return nil
	},
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
