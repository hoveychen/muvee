package main

// `muveectl projects env` — list the env vars that are actually set inside
// the running project container, with secret-looking values masked by default.
// Useful for confirming that a Muvee secret/binding actually reached the
// container without having to exec in.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

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
	Short: "List the environment variables effective inside the project container",
	Long: `Reads the running container's env via 'docker inspect'. Values with
secret-looking keys (PASSWORD/SECRET/TOKEN/KEY/CREDENTIAL/...) are masked as
'***' by default; pass --raw to see the unmasked values. The dispatch goes
through a one-shot agent task so no shell is opened inside the container.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		raw, _ := cmd.Flags().GetBool("raw")
		timeout, _ := cmd.Flags().GetDuration("timeout")

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
