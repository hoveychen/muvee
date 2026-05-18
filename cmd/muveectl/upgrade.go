package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Download and atomically replace the current muveectl binary",
	Long: `Downloads the latest muveectl binary from the configured server's
/api/muveectl/<asset> endpoint and atomically replaces the running binary in
place. The current profile's server URL is used; switch profiles with
'muveectl profile use' to upgrade from a different environment.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cl.server == "" {
			return fmt.Errorf("no server configured; run 'muveectl login --server <url>' first")
		}

		binPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate current binary: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
			binPath = resolved
		}

		asset := fmt.Sprintf("muveectl_%s_%s", runtime.GOOS, runtime.GOARCH)
		if runtime.GOOS == "windows" {
			asset += ".exe"
		}
		url := strings.TrimRight(cl.server, "/") + "/api/muveectl/" + asset

		fmt.Fprintf(os.Stderr, "Downloading %s from %s...\n", asset, cl.server)
		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("download %s: %s", url, resp.Status)
		}

		dir := filepath.Dir(binPath)
		tmp, err := os.CreateTemp(dir, ".muveectl-upgrade-*")
		if err != nil {
			return fmt.Errorf("create temp file in %s: %w (need write access to upgrade in place)", dir, err)
		}
		tmpPath := tmp.Name()
		cleanup := func() { os.Remove(tmpPath) }
		if _, err := io.Copy(tmp, resp.Body); err != nil {
			tmp.Close()
			cleanup()
			return fmt.Errorf("write download: %w", err)
		}
		if err := tmp.Close(); err != nil {
			cleanup()
			return fmt.Errorf("close temp: %w", err)
		}
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			cleanup()
			return fmt.Errorf("chmod temp: %w", err)
		}
		if err := os.Rename(tmpPath, binPath); err != nil {
			cleanup()
			return fmt.Errorf("install %s: %w", binPath, err)
		}

		fmt.Fprintf(os.Stderr, "muveectl upgraded in place at %s (was %s).\n", binPath, version)
		fmt.Fprintln(os.Stderr, "Run 'muveectl version' to confirm the new version.")
		return nil
	},
}
