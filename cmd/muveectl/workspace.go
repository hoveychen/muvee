package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage project workspace files",
	Long:  "Persistent storage attached to the running container, accessible at /workspace inside the container.",
}

func init() {
	projectsCmd.AddCommand(workspaceCmd)

	workspaceCmd.AddCommand(workspaceLsCmd)
	workspaceCmd.AddCommand(workspacePullCmd)
	workspaceCmd.AddCommand(workspacePushCmd)
	workspaceCmd.AddCommand(workspaceRmCmd)

	// workspace push flags
	workspacePushCmd.Flags().String("remote-path", "", "Remote destination path")
}

// ─── Ls ──────────────────────────────────────────────────────────────────────

var workspaceLsCmd = &cobra.Command{
	Use:   "ls PROJECT_ID [path]",
	Short: "List files in the workspace root (or a subdirectory)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		path := ""
		if len(args) > 1 {
			path = args[1]
		}
		url := "/api/projects/" + projectID + "/workspace"
		if path != "" {
			url += "?path=" + urlEscape(path)
		}
		items, err := cl.doArray("GET", url, nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("(empty)")
			return nil
		}
		for _, item := range items {
			m, _ := item.(map[string]interface{})
			if m == nil {
				continue
			}
			isDir, _ := m["is_dir"].(bool)
			name := str(m, "name")
			if isDir {
				name += "/"
			}
			size := str(m, "size")
			if isDir {
				size = "-"
			}
			fmt.Printf("%-40s %10s\n", name, size)
		}
		return nil
	},
}

// ─── Pull ────────────────────────────────────────────────────────────────────

var workspacePullCmd = &cobra.Command{
	Use:   "pull PROJECT_ID REMOTE_PATH [LOCAL_PATH]",
	Short: "Download a file from the workspace",
	Args:  cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		remotePath := args[1]
		localPath := filepath.Base(remotePath)
		if len(args) >= 3 {
			localPath = args[2]
		}
		url := cl.server + "/api/projects/" + projectID + "/workspace/download?path=" + urlEscape(remotePath)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		if cl.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+cl.cfg.Token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		out, err := os.Create(localPath)
		if err != nil {
			return fmt.Errorf("create local file: %w", err)
		}
		defer out.Close()
		n, err := io.Copy(out, resp.Body)
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		fmt.Printf("Downloaded %s → %s (%d bytes)\n", remotePath, localPath, n)
		return nil
	},
}

// ─── Push ────────────────────────────────────────────────────────────────────

var workspacePushCmd = &cobra.Command{
	Use:   "push PROJECT_ID LOCAL_FILE",
	Short: "Upload a local file to the workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		localFile := args[1]
		remotePath, _ := cmd.Flags().GetString("remote-path")

		f, err := os.Open(localFile)
		if err != nil {
			return fmt.Errorf("open file: %w", err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}

		// Build multipart body
		pr, pw := io.Pipe()
		mw := newMultipartWriter(pw)
		go func() {
			fw, werr := mw.CreateFormFile("file", info.Name())
			if werr == nil {
				_, _ = io.Copy(fw, f)
			}
			_ = mw.Close()
			_ = pw.Close()
		}()

		url := cl.server + "/api/projects/" + projectID + "/workspace/upload"
		if remotePath != "" {
			url += "?path=" + urlEscape(remotePath)
		}
		req, err := http.NewRequest("POST", url, pr)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if cl.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+cl.cfg.Token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		fmt.Printf("Uploaded %s → workspace:%s\n", localFile, remotePath)
		return nil
	},
}

// ─── Rm ──────────────────────────────────────────────────────────────────────

var workspaceRmCmd = &cobra.Command{
	Use:   "rm PROJECT_ID REMOTE_PATH",
	Short: "Delete a file from the workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID := args[0]
		remotePath := args[1]
		url := "/api/projects/" + projectID + "/workspace?path=" + urlEscape(remotePath)
		_, err := cl.do("DELETE", url, nil)
		if err != nil {
			return err
		}
		fmt.Printf("Deleted workspace:%s\n", remotePath)
		return nil
	},
}
