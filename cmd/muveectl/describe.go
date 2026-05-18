package main

// `muveectl projects describe` — kubectl-describe-style snapshot of a project
// container's status. Pulls the data via a one-shot agent task that runs
// `docker inspect`, then renders it as human-readable text. For machine
// consumption, use --output=json to dump the raw inspect summary.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var projectsDescribeCmd = &cobra.Command{
	Use:   "describe ID-OR-NAME",
	Short: "Show container state: restart count, last exit reason, image, ports, mounts",
	Long: `Aggregates 'docker inspect' into a kubectl-describe-style report.
The first place to look when a container is crash-looping or behaving
unexpectedly: see Status, ExitCode, OOMKilled, RestartCount, Health, and
the image sha that's actually running.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		resp, err := cl.do("POST", "/api/projects/"+projectID+"/describe", nil)
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

		if output == "json" {
			fmt.Println(result)
			return nil
		}

		var summary map[string]interface{}
		if err := json.Unmarshal([]byte(result), &summary); err != nil {
			return fmt.Errorf("parse describe: %w", err)
		}
		renderDescribe(summary)
		return nil
	},
}

func renderDescribe(s map[string]interface{}) {
	w := newKVWriter()
	w.add("Container", str(s, "container_name"))
	w.add("Image", str(s, "image"))
	if sha := str(s, "image_sha"); sha != "" {
		w.add("Image SHA", sha)
	}
	if cmd, ok := s["command"].([]interface{}); ok {
		parts := make([]string, 0, len(cmd))
		for _, p := range cmd {
			if str, ok := p.(string); ok && str != "" {
				parts = append(parts, str)
			}
		}
		if len(parts) > 0 {
			w.add("Command", strings.Join(parts, " "))
		}
	}
	w.add("Created", str(s, "created_at"))
	if rc, ok := s["restart_count"].(float64); ok {
		w.add("Restart Count", fmt.Sprintf("%d", int(rc)))
	}
	if mem, ok := s["memory_limit"].(float64); ok && mem > 0 {
		w.add("Memory Limit", fmt.Sprintf("%d bytes", int64(mem)))
	}
	w.flush("Identity")

	if state, ok := s["state"].(map[string]interface{}); ok {
		w.reset()
		w.add("Status", str(state, "status"))
		if v, ok := state["running"].(bool); ok {
			w.add("Running", fmt.Sprintf("%v", v))
		}
		if v, ok := state["oom_killed"].(bool); ok && v {
			w.add("OOMKilled", "true")
		}
		if v, ok := state["exit_code"].(float64); ok {
			w.add("Exit Code", fmt.Sprintf("%d", int(v)))
		}
		if v := str(state, "error"); v != "" {
			w.add("Error", v)
		}
		w.add("Started At", str(state, "started_at"))
		if v := str(state, "finished_at"); v != "" && !strings.HasPrefix(v, "0001-01-01") {
			w.add("Finished At", v)
		}
		if v := str(state, "health"); v != "" {
			w.add("Health", v)
		}
		w.flush("State")
	}

	if ports, ok := s["ports"].(map[string]interface{}); ok && len(ports) > 0 {
		w.reset()
		keys := make([]string, 0, len(ports))
		for k := range ports {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			bindings, _ := ports[k].([]interface{})
			if len(bindings) == 0 {
				w.add(k, "(exposed, no host binding)")
				continue
			}
			var lines []string
			for _, b := range bindings {
				bm, _ := b.(map[string]interface{})
				lines = append(lines, fmt.Sprintf("%s:%s", str(bm, "HostIp"), str(bm, "HostPort")))
			}
			w.add(k, strings.Join(lines, ", "))
		}
		w.flush("Ports")
	}

	if mounts, ok := s["mounts"].([]interface{}); ok && len(mounts) > 0 {
		w.reset()
		for _, m := range mounts {
			mm, _ := m.(map[string]interface{})
			mode := str(mm, "Mode")
			if mode == "" {
				if rw, _ := mm["RW"].(bool); rw {
					mode = "rw"
				} else {
					mode = "ro"
				}
			}
			line := fmt.Sprintf("%s (%s, %s)", str(mm, "Source"), str(mm, "Type"), mode)
			w.add(str(mm, "Destination"), line)
		}
		w.flush("Mounts")
	}

	if keys, ok := s["env_keys"].([]interface{}); ok && len(keys) > 0 {
		ks := make([]string, 0, len(keys))
		for _, k := range keys {
			if s, ok := k.(string); ok {
				ks = append(ks, s)
			}
		}
		sort.Strings(ks)
		fmt.Println()
		fmt.Println("== Env Keys ==")
		fmt.Println(strings.Join(ks, "  "))
		fmt.Println("\n(use 'muveectl projects env ID' to see values)")
	}
}

// kvWriter accumulates label/value pairs and prints them as a left-aligned
// key column. Reused per section so describe output stays uniform.
type kvWriter struct {
	rows [][2]string
	maxK int
}

func newKVWriter() *kvWriter { return &kvWriter{} }

func (w *kvWriter) reset() {
	w.rows = nil
	w.maxK = 0
}

func (w *kvWriter) add(k, v string) {
	if v == "" {
		return
	}
	w.rows = append(w.rows, [2]string{k, v})
	if len(k) > w.maxK {
		w.maxK = len(k)
	}
}

func (w *kvWriter) flush(section string) {
	if len(w.rows) == 0 {
		return
	}
	fmt.Println()
	fmt.Printf("== %s ==\n", section)
	for _, row := range w.rows {
		fmt.Printf("%-*s  %s\n", w.maxK, row[0], row[1])
	}
	w.rows = nil
	w.maxK = 0
}
