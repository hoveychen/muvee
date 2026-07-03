package main

// `muveectl projects pause` / `resume` — soft-stop or restart a project's
// container(s) without losing its config. Pause issues 'docker stop' on the
// deploy node (CPU/memory freed, image+volumes kept) and gates every deploy
// path; resume issues 'docker start' — no rebuild, no redeploy. Both dispatch
// an agent task and poll /api/tasks/{id} for completion when one is returned.

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var projectsPauseCmd = &cobra.Command{
	Use:   "pause ID-OR-NAME",
	Short: "Soft-pause the project (docker stop; config kept, deploys blocked)",
	Long: `Stops the project's running container(s) via 'docker stop' on the
deploy node. CPU and memory are released; the container, its image, and its
named volumes are kept, so resume is a plain 'docker start' with no rebuild.

While paused, every deploy path (manual deploy, auto-deploy poller, image
watcher, git-push hook) refuses to redeploy the project until it is resumed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPauseResume(cmd, args[0], "pause")
	},
}

var projectsResumeCmd = &cobra.Command{
	Use:   "resume ID-OR-NAME",
	Short: "Resume a paused project (docker start; no rebuild)",
	Long: `Starts the project's stopped container(s) via 'docker start' on the
deploy node and clears the paused flag so deploys are allowed again. No
rebuild, no re-pull — the previously paused container resumes as-is.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPauseResume(cmd, args[0], "resume")
	},
}

// runPauseResume POSTs to the pause/resume endpoint, then — if the server
// dispatched a stop/start task — polls it to completion. A project with no
// running deployment returns no task_id; the state change still succeeds.
func runPauseResume(cmd *cobra.Command, ref, action string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	projectID, err := resolveProjectRef(cl, ref)
	if err != nil {
		return err
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")

	resp, err := cl.do("POST", "/api/projects/"+projectID+"/"+action, nil)
	if err != nil {
		return err
	}
	// Surface the node-offline warning before polling: the task stays queued
	// until the agent reconnects, so the poll below is likely to time out.
	if warn := str(resp, "warning"); warn != "" {
		fmt.Println("warning: " + warn)
	}
	taskID := str(resp, "task_id")
	if taskID == "" {
		// No running container to act on; the paused flag was still updated.
		fmt.Printf("%s: %s (no container task dispatched)\n", action, str(resp, "status"))
		return nil
	}
	out, err := waitTaskResult(cl, taskID, timeout)
	if err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}

func init() {
	projectsCmd.AddCommand(projectsPauseCmd)
	projectsPauseCmd.Flags().Duration("timeout", 60*time.Second, "Max wait for the pause task to complete")
	projectsCmd.AddCommand(projectsResumeCmd)
	projectsResumeCmd.Flags().Duration("timeout", 60*time.Second, "Max wait for the resume task to complete")
}
