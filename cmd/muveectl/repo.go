package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Browse hosted git repository contents",
}

func init() {
	projectsCmd.AddCommand(repoCmd)

	repoCmd.AddCommand(repoTreeCmd)
	repoCmd.AddCommand(repoLogCmd)
	repoCmd.AddCommand(repoBranchesCmd)
	repoCmd.AddCommand(repoShowCmd)

	// repo tree flags
	repoTreeCmd.Flags().String("ref", "", "Git reference (branch/tag/commit)")
	repoTreeCmd.Flags().String("path", "", "Directory path")

	// repo log flags
	repoLogCmd.Flags().String("ref", "", "Git reference")
	repoLogCmd.Flags().Int("limit", 20, "Commit limit")
}

// ─── Tree ────────────────────────────────────────────────────────────────────

var repoTreeCmd = &cobra.Command{
	Use:   "tree PROJECT_ID",
	Short: "List directory entries at a given ref and path",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		ref, _ := cmd.Flags().GetString("ref")
		path, _ := cmd.Flags().GetString("path")

		params := "?"
		if ref != "" {
			params += "ref=" + urlEscape(ref) + "&"
		}
		if path != "" {
			params += "path=" + urlEscape(path) + "&"
		}
		result, err := cl.doRaw("GET", "/api/projects/"+projectID+"/repo/tree"+params, nil)
		if err != nil {
			return err
		}
		var entries []map[string]interface{}
		if err := json.Unmarshal(result, &entries); err != nil {
			return err
		}
		if jsonMode {
			printJSON(entries)
			return nil
		}
		for _, e := range entries {
			t := str(e, "type")
			name := str(e, "name")
			if t == "tree" {
				fmt.Printf("  %s/\n", name)
			} else {
				fmt.Printf("  %s\n", name)
			}
		}
		return nil
	},
}

// ─── Log ─────────────────────────────────────────────────────────────────────

var repoLogCmd = &cobra.Command{
	Use:   "log PROJECT_ID",
	Short: "Show recent commits",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		ref, _ := cmd.Flags().GetString("ref")
		limit, _ := cmd.Flags().GetInt("limit")

		params := fmt.Sprintf("?limit=%d", limit)
		if ref != "" {
			params += "&ref=" + urlEscape(ref)
		}
		result, err := cl.doRaw("GET", "/api/projects/"+projectID+"/repo/commits"+params, nil)
		if err != nil {
			return err
		}
		var commits []map[string]interface{}
		if err := json.Unmarshal(result, &commits); err != nil {
			return err
		}
		if jsonMode {
			printJSON(commits)
			return nil
		}
		for _, cm := range commits {
			sha := str(cm, "sha")
			if len(sha) > 8 {
				sha = sha[:8]
			}
			fmt.Printf("%s  %s  (%s, %s)\n", sha, str(cm, "message"), str(cm, "author"), str(cm, "date"))
		}
		return nil
	},
}

// ─── Branches ────────────────────────────────────────────────────────────────

var repoBranchesCmd = &cobra.Command{
	Use:   "branches PROJECT_ID",
	Short: "List branches in the hosted repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		result, err := cl.doRaw("GET", "/api/projects/"+projectID+"/repo/branches", nil)
		if err != nil {
			return err
		}
		var branches []map[string]interface{}
		if err := json.Unmarshal(result, &branches); err != nil {
			return err
		}
		if jsonMode {
			printJSON(branches)
			return nil
		}
		for _, b := range branches {
			name := str(b, "name")
			if b["is_default"] == true {
				fmt.Printf("* %s\n", name)
			} else {
				fmt.Printf("  %s\n", name)
			}
		}
		return nil
	},
}

// ─── Show ────────────────────────────────────────────────────────────────────

var repoShowCmd = &cobra.Command{
	Use:   "show PROJECT_ID REF:PATH",
	Short: "Show file content (e.g. main:README.md)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		refPath := args[1]
		// refPath format: ref:path, e.g. "main:README.md"
		parts := strings.SplitN(refPath, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("expected format: <ref>:<path> (e.g. main:README.md)")
		}
		ref, path := parts[0], parts[1]
		params := "?ref=" + urlEscape(ref) + "&path=" + urlEscape(path)
		result, err := cl.doRaw("GET", "/api/projects/"+projectID+"/repo/blob"+params, nil)
		if err != nil {
			return err
		}
		os.Stdout.Write(result)
		return nil
	},
}
