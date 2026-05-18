package main

// `muveectl projects restart` — kick the project's running container without
// rebuilding or redeploying. Dispatches a restart task to the deploy node's
// agent, polls /api/tasks/{id} until it completes, prints the result.

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var projectsRestartCmd = &cobra.Command{
	Use:   "restart ID-OR-NAME",
	Short: "Restart the project's running container (no rebuild, no redeploy)",
	Long: `Issues 'docker restart' against the running container on the deploy
node. Useful for picking up environment-variable changes, clearing memory,
or unblocking a wedged process without going through a full deploy cycle.

The command dispatches an agent task and polls for completion (typically
under 5 s with the agent's task poll interval). On failure the agent's
stderr is printed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")

		resp, err := cl.do("POST", "/api/projects/"+projectID+"/restart", nil)
		if err != nil {
			return err
		}
		taskID := str(resp, "task_id")
		if taskID == "" {
			return fmt.Errorf("server did not return task_id")
		}

		deadline := time.Now().Add(timeout)
		for {
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for restart task %s", taskID)
			}
			task, err := cl.do("GET", "/api/tasks/"+taskID, nil)
			if err != nil {
				return err
			}
			switch str(task, "status") {
			case "completed":
				out := str(task, "result")
				if out != "" {
					fmt.Println(out)
				}
				return nil
			case "failed":
				out := str(task, "result")
				if out == "" {
					out = "(no error message)"
				}
				return fmt.Errorf("restart failed: %s", out)
			}
			time.Sleep(500 * time.Millisecond)
		}
	},
}
