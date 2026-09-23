package main

// `muveectl projects env` — manage a project's environment variables (KEY,
// value, sensitive flag, runtime/build scope). `--live` instead lists the env
// vars actually set inside the running container, with secret-looking values
// masked by default.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	projectsCmd.AddCommand(projectsEnvCmd)
	projectsEnvCmd.Flags().Bool("live", false, "List the env effective inside the running container instead of the configured variables")
	projectsEnvCmd.Flags().Bool("raw", false, "With --live: show env values unmasked (still gated by project access)")
	projectsEnvCmd.Flags().Duration("timeout", 60*time.Second, "With --live: max wait for the env task to complete")

	projectsEnvCmd.AddCommand(projectsEnvSetCmd)
	projectsEnvCmd.AddCommand(projectsEnvUnsetCmd)
	projectsEnvCmd.AddCommand(projectsEnvCopyCmd)

	projectsEnvSetCmd.Flags().Bool("plain", false, "Store as non-sensitive (value readable by project members); new variables only")
	projectsEnvSetCmd.Flags().Bool("build", false, "Also expose as a docker build secret (RUN --mount=type=secret,id=KEY)")
	projectsEnvSetCmd.Flags().Bool("no-runtime", false, "Do not inject into the container environment")

	projectsEnvCopyCmd.Flags().String("secret-id", "", "Personal secret ID to copy (required)")
	projectsEnvCopyCmd.Flags().String("key", "", "Variable name (default: derived from the secret name)")
	projectsEnvCopyCmd.Flags().Bool("build", false, "Also expose as a docker build secret")
	projectsEnvCopyCmd.Flags().Bool("no-runtime", false, "Do not inject into the container environment")
	projectsEnvCopyCmd.MarkFlagRequired("secret-id")
}

var envSecretSubstrings = []string{
	"PASSWORD",
	"SECRET",
	"TOKEN",
	"KEY",
	"CREDENTIAL",
	"PASSPHRASE",
	"PRIVATE",
	"DSN",
}

func isSecretEnvKey(k string) bool {
	upper := strings.ToUpper(k)
	for _, sub := range envSecretSubstrings {
		if strings.Contains(upper, sub) {
			return true
		}
	}
	return false
}

var projectsEnvCmd = &cobra.Command{
	Use:   "env ID-OR-NAME",
	Short: "List a project's environment variables (or the live container env with --live)",
	Long: `Lists the project's configured variables. Sensitive values are never
returned — VALUE_STATUS / VALUE_LENGTH show whether a value is set. Non-sensitive
values are shown in full.

With --live, reads the running container's env via 'docker inspect' instead.
Values with secret-looking keys (PASSWORD/SECRET/TOKEN/KEY/CREDENTIAL/...) are
masked as '***' by default; pass --raw to see the unmasked values. The dispatch
goes through a one-shot agent task so no shell is opened inside the container.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		if live, _ := cmd.Flags().GetBool("live"); live {
			raw, _ := cmd.Flags().GetBool("raw")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			return printLiveEnv(projectID, raw, timeout)
		}
		items, err := cl.doArray("GET", "/api/projects/"+projectID+"/env-vars", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("This project has no environment variables.")
			return nil
		}
		printTable(items, []string{"key", "sensitive", "runtime", "build", "value_status", "value_length", "value"})
		return nil
	},
}

func printLiveEnv(projectID string, raw bool, timeout time.Duration) error {
	resp, err := cl.do("POST", "/api/projects/"+projectID+"/env", nil)
	if err != nil {
		return err
	}
	taskID := str(resp, "task_id")
	if taskID == "" {
		return fmt.Errorf("server did not return task_id")
	}

	result, err := waitTaskResult(cl, taskID, timeout)
	if err != nil {
		return err
	}

	var env map[string]string
	if err := json.Unmarshal([]byte(result), &env); err != nil {
		return fmt.Errorf("parse agent result: %w", err)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := env[k]
		if !raw && isSecretEnvKey(k) {
			v = "***"
		}
		fmt.Printf("%s=%s\n", k, v)
	}
	return nil
}

// findProjectEnvVar returns the variable with the given key, or nil.
func findProjectEnvVar(projectID, key string) (map[string]interface{}, error) {
	items, err := cl.doArray("GET", "/api/projects/"+projectID+"/env-vars", nil)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if m, _ := item.(map[string]interface{}); m != nil && str(m, "key") == key {
			return m, nil
		}
	}
	return nil, nil
}

func printEnvVarResult(action string, result map[string]interface{}) {
	if jsonMode {
		printJSON(result)
		return
	}
	fmt.Printf("%s %s (sensitive: %s, runtime: %s, build: %s, value: %s)\n", action,
		str(result, "key"), str(result, "sensitive"), str(result, "runtime"), str(result, "build"), str(result, "value_status"))
}

var projectsEnvSetCmd = &cobra.Command{
	Use:   "set ID-OR-NAME KEY=VALUE",
	Short: "Create or update a project variable (sensitive by default)",
	Long: `Creates KEY if it does not exist, otherwise replaces its value. New
variables are sensitive (write-only) unless --plain is given; an existing
sensitive variable cannot be made plain. --build / --no-runtime set the scope;
for an existing variable the scope is only changed when the flag is given
(e.g. --build=false, --no-runtime=false).`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		key, value, ok := strings.Cut(args[1], "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || value == "" {
			return fmt.Errorf("expected KEY=VALUE with a non-empty value")
		}
		plain, _ := cmd.Flags().GetBool("plain")
		build, _ := cmd.Flags().GetBool("build")
		noRuntime, _ := cmd.Flags().GetBool("no-runtime")

		existing, err := findProjectEnvVar(projectID, key)
		if err != nil {
			return err
		}
		if existing == nil {
			result, err := cl.do("POST", "/api/projects/"+projectID+"/env-vars", map[string]interface{}{
				"key":       key,
				"value":     value,
				"sensitive": !plain,
				"runtime":   !noRuntime,
				"build":     build,
			})
			if err != nil {
				return err
			}
			printEnvVarResult("Created", result)
			return nil
		}
		// Only touch the scope of an existing variable when asked to.
		patch := map[string]interface{}{"value": value}
		if cmd.Flags().Changed("no-runtime") {
			patch["runtime"] = !noRuntime
		}
		if cmd.Flags().Changed("build") {
			patch["build"] = build
		}
		if plain && str(existing, "sensitive") == "true" {
			return fmt.Errorf("%s is sensitive and cannot be made plain; unset and set it again instead", key)
		}
		result, err := cl.do("PATCH", "/api/projects/"+projectID+"/env-vars/"+str(existing, "id"), patch)
		if err != nil {
			return err
		}
		printEnvVarResult("Updated", result)
		return nil
	},
}

var projectsEnvUnsetCmd = &cobra.Command{
	Use:   "unset ID-OR-NAME KEY",
	Short: "Delete a project variable",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		existing, err := findProjectEnvVar(projectID, args[1])
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("variable %s not found in project", args[1])
		}
		if _, err := cl.do("DELETE", "/api/projects/"+projectID+"/env-vars/"+str(existing, "id"), nil); err != nil {
			return err
		}
		fmt.Printf("Deleted %s\n", args[1])
		return nil
	},
}

var projectsEnvCopyCmd = &cobra.Command{
	Use:   "copy ID-OR-NAME --secret-id ID",
	Short: "Copy one of your personal secrets into the project as a variable",
	Long: `Copies the personal secret's current value into a new project variable.
The copy is independent: later edits to the personal secret do not affect it.
env_var secrets become plain variables, other types sensitive.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		secretID, _ := cmd.Flags().GetString("secret-id")
		key, _ := cmd.Flags().GetString("key")
		build, _ := cmd.Flags().GetBool("build")
		noRuntime, _ := cmd.Flags().GetBool("no-runtime")
		result, err := cl.do("POST", "/api/projects/"+projectID+"/env-vars", map[string]interface{}{
			"from_secret_id": secretID,
			"key":            strings.TrimSpace(key),
			"runtime":        !noRuntime,
			"build":          build,
		})
		if err != nil {
			return err
		}
		printEnvVarResult("Copied", result)
		return nil
	},
}

// waitTaskResult polls /api/tasks/{id} until the task completes/fails or the
// timeout elapses. Shared by env / describe / restart-style dispatched tasks.
func waitTaskResult(c *client, taskID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for task %s", taskID)
		}
		task, err := c.do("GET", "/api/tasks/"+taskID, nil)
		if err != nil {
			return "", err
		}
		switch str(task, "status") {
		case "completed":
			return str(task, "result"), nil
		case "failed":
			msg := str(task, "result")
			if msg == "" {
				msg = "(no error message)"
			}
			return "", fmt.Errorf("task failed: %s", msg)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
