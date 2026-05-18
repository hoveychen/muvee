package main

// `muveectl projects events` — tail the in-memory platform-event ring buffer
// for a project. Events cover deploy lifecycle, restarts, and OOMs; for raw
// stdout/stderr, see `projects runtime-logs`.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type projectEvent struct {
	ID       int64     `json:"id"`
	Type     string    `json:"type"`
	Severity string    `json:"severity"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

var projectsEventsCmd = &cobra.Command{
	Use:   "events ID-OR-NAME",
	Short: "Show platform events for a project (deploy lifecycle, restarts, OOMs)",
	Long: `Returns events recorded server-side: deploy started/completed/failed,
restart requests, and (when wired) container OOM kills. With --follow, the
command polls every 3 s and prints new events as they arrive (Ctrl-C to
stop). Events live in an in-memory ring buffer capped at 200 per project
and are lost on server restart.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		follow, _ := cmd.Flags().GetBool("follow")
		limit, _ := cmd.Flags().GetInt("limit")

		var since int64
		for {
			path := fmt.Sprintf("/api/projects/%s/events?since=%d&limit=%d", projectID, since, limit)
			resp, err := cl.do("GET", path, nil)
			if err != nil {
				return err
			}
			rawList, _ := resp["events"].([]interface{})
			for _, raw := range rawList {
				b, _ := json.Marshal(raw)
				var e projectEvent
				if err := json.Unmarshal(b, &e); err != nil {
					continue
				}
				printEvent(e)
				if e.ID > since {
					since = e.ID
				}
			}
			if !follow {
				return nil
			}
			time.Sleep(3 * time.Second)
		}
	},
}

func printEvent(e projectEvent) {
	fmt.Printf("%s  [%-5s]  %-25s  %s\n",
		e.At.Local().Format("2006-01-02 15:04:05"),
		e.Severity, e.Type, e.Message)
}
