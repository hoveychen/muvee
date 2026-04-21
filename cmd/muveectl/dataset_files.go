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

func init() {
	datasetsCmd.AddCommand(datasetLsCmd)
	datasetsCmd.AddCommand(datasetPullCmd)
	datasetsCmd.AddCommand(datasetPushCmd)
	datasetsCmd.AddCommand(datasetRmCmd)
	datasetsCmd.AddCommand(datasetMkdirCmd)
	datasetsCmd.AddCommand(datasetMvCmd)
	datasetsCmd.AddCommand(datasetCpCmd)

	// dataset push flags
	datasetPushCmd.Flags().String("remote-path", "", "Remote destination path")
}

// ─── Ls ─────────────────────────────────────────────────────────────────────

var datasetLsCmd = &cobra.Command{
	Use:   "ls DATASET_ID [path]",
	Short: "List files in the dataset root (or a subdirectory)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		path := ""
		if len(args) > 1 {
			path = args[1]
		}
		url := "/api/datasets/" + datasetID + "/files"
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

// ─── Pull ───────────────────────────────────────────────────────────────────

var datasetPullCmd = &cobra.Command{
	Use:   "pull DATASET_ID REMOTE_PATH [LOCAL_PATH]",
	Short: "Download a file from the dataset",
	Args:  cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		remotePath := args[1]
		localPath := filepath.Base(remotePath)
		if len(args) >= 3 {
			localPath = args[2]
		}
		url := cl.server + "/api/datasets/" + datasetID + "/files/download?path=" + urlEscape(remotePath)
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

// ─── Push ───────────────────────────────────────────────────────────────────

var datasetPushCmd = &cobra.Command{
	Use:   "push DATASET_ID LOCAL_FILE",
	Short: "Upload a local file to the dataset",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
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

		url := cl.server + "/api/datasets/" + datasetID + "/files/upload"
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
		dest := remotePath
		if dest == "" {
			dest = "/"
		}
		fmt.Printf("Uploaded %s → dataset:%s\n", localFile, dest)
		return nil
	},
}

// ─── Rm ─────────────────────────────────────────────────────────────────────

var datasetRmCmd = &cobra.Command{
	Use:   "rm DATASET_ID PATH",
	Short: "Delete a file or directory from the dataset",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		remotePath := args[1]
		url := "/api/datasets/" + datasetID + "/files?path=" + urlEscape(remotePath)
		_, err := cl.do("DELETE", url, nil)
		if err != nil {
			return err
		}
		fmt.Printf("Deleted dataset:%s\n", remotePath)
		return nil
	},
}

// ─── Mkdir ──────────────────────────────────────────────────────────────────

var datasetMkdirCmd = &cobra.Command{
	Use:   "mkdir DATASET_ID PATH",
	Short: "Create a directory in the dataset",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		path := args[1]
		body := map[string]interface{}{"path": path}
		_, err := cl.do("POST", "/api/datasets/"+datasetID+"/files/mkdir", body)
		if err != nil {
			return err
		}
		fmt.Printf("Created directory dataset:%s\n", path)
		return nil
	},
}

// ─── Mv ─────────────────────────────────────────────────────────────────────

var datasetMvCmd = &cobra.Command{
	Use:   "mv DATASET_ID SRC DST",
	Short: "Move or rename a file/directory in the dataset",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		body := map[string]interface{}{"src": args[1], "dst": args[2]}
		_, err := cl.do("POST", "/api/datasets/"+datasetID+"/files/move", body)
		if err != nil {
			return err
		}
		fmt.Printf("Moved dataset:%s → dataset:%s\n", args[1], args[2])
		return nil
	},
}

// ─── Cp ─────────────────────────────────────────────────────────────────────

var datasetCpCmd = &cobra.Command{
	Use:   "cp DATASET_ID SRC DST",
	Short: "Copy a file in the dataset",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		datasetID := args[0]
		body := map[string]interface{}{"src": args[1], "dst": args[2]}
		_, err := cl.do("POST", "/api/datasets/"+datasetID+"/files/copy", body)
		if err != nil {
			return err
		}
		fmt.Printf("Copied dataset:%s → dataset:%s\n", args[1], args[2])
		return nil
	},
}
