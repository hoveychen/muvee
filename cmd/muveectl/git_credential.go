package main

// `muveectl projects git-credential` — view / set the credential muvee uses to
// clone a project's external git repository.

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	projectsCmd.AddCommand(projectsGitCredentialCmd)
	projectsGitCredentialCmd.AddCommand(projectsGitCredentialSetCmd)
	projectsGitCredentialCmd.AddCommand(projectsGitCredentialClearCmd)

	projectsGitCredentialSetCmd.Flags().String("type", "", "Auth type: https_token, ssh_key, or none")
	projectsGitCredentialSetCmd.Flags().String("username", "", "HTTPS username (default: x-access-token, fits GitHub PATs)")
	projectsGitCredentialSetCmd.Flags().String("value", "", "Token or SSH private key")
	projectsGitCredentialSetCmd.Flags().String("value-file", "", "Read the token / SSH private key from a file")
	projectsGitCredentialSetCmd.Flags().String("from-secret-id", "", "Copy one of your personal password / ssh_key secrets instead of --type/--value")
}

func printGitCredential(result map[string]interface{}) {
	if jsonMode {
		printJSON(result)
		return
	}
	typ := str(result, "type")
	if typ == "" || typ == "none" {
		fmt.Println("Type:  none (cloned anonymously)")
		return
	}
	fmt.Printf("Type:      %s\n", typ)
	if u := str(result, "username"); u != "" {
		fmt.Printf("Username:  %s\n", u)
	}
	status := str(result, "value_status")
	if status == "set" {
		status += fmt.Sprintf(" (%s chars)", str(result, "value_length"))
	}
	fmt.Printf("Value:     %s\n", status)
}

var projectsGitCredentialCmd = &cobra.Command{
	Use:   "git-credential ID-OR-NAME",
	Short: "Show the credential used to clone the project's external git repo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/projects/"+projectID+"/git-credential", nil)
		if err != nil {
			return err
		}
		printGitCredential(result)
		return nil
	},
}

var projectsGitCredentialSetCmd = &cobra.Command{
	Use:   "set ID-OR-NAME --type https_token|ssh_key|none [--username U] [--value V | --value-file F]",
	Short: "Set the git clone credential (or copy it from a personal secret with --from-secret-id)",
	Long: `Sets how muvee authenticates when cloning the project's external repo.
The value is write-only. Omitting --value keeps the stored value, which is
allowed only when the type is unchanged (e.g. to change just the username).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		typ, _ := cmd.Flags().GetString("type")
		username, _ := cmd.Flags().GetString("username")
		value, _ := cmd.Flags().GetString("value")
		valueFile, _ := cmd.Flags().GetString("value-file")
		fromSecretID, _ := cmd.Flags().GetString("from-secret-id")

		var body map[string]interface{}
		if fromSecretID != "" {
			if typ != "" || value != "" || valueFile != "" {
				return fmt.Errorf("--from-secret-id cannot be combined with --type / --value / --value-file")
			}
			body = map[string]interface{}{"from_secret_id": fromSecretID, "username": username}
		} else {
			if typ == "" {
				return fmt.Errorf("--type is required (https_token, ssh_key, or none)")
			}
			if valueFile != "" {
				data, err := os.ReadFile(valueFile)
				if err != nil {
					return fmt.Errorf("read value file: %w", err)
				}
				value = string(data)
			}
			body = map[string]interface{}{"type": typ, "username": strings.TrimSpace(username), "value": value}
		}
		result, err := cl.do("PUT", "/api/projects/"+projectID+"/git-credential", body)
		if err != nil {
			return err
		}
		printGitCredential(result)
		return nil
	},
}

var projectsGitCredentialClearCmd = &cobra.Command{
	Use:   "clear ID-OR-NAME",
	Short: "Remove the git clone credential (clone anonymously)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		result, err := cl.do("PUT", "/api/projects/"+projectID+"/git-credential", map[string]interface{}{"type": "none"})
		if err != nil {
			return err
		}
		printGitCredential(result)
		return nil
	},
}
