package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var datasetsCmd = &cobra.Command{
	Use:   "datasets",
	Short: "Manage datasets",
}

func init() {
	rootCmd.AddCommand(datasetsCmd)

	datasetsCmd.AddCommand(datasetsListCmd)
	datasetsCmd.AddCommand(datasetsCreateCmd)
	datasetsCmd.AddCommand(datasetsGetCmd)
	datasetsCmd.AddCommand(datasetsScanCmd)
	datasetsCmd.AddCommand(datasetsDeleteCmd)

	// datasets create flags
	datasetsCreateCmd.Flags().String("name", "", "Dataset name (required)")
	datasetsCreateCmd.Flags().String("nfs-path", "", "NFS mount path (required)")
	datasetsCreateCmd.MarkFlagRequired("name")
	datasetsCreateCmd.MarkFlagRequired("nfs-path")
}

// ─── List ────────────────────────────────────────────────────────────────────

var datasetsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List datasets",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/datasets", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No datasets found.")
			return nil
		}
		printTable(items, []string{"id", "name", "nfs_path", "version"})
		return nil
	},
}

// ─── Create ──────────────────────────────────────────────────────────────────

var datasetsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a dataset",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		nfsPath, _ := cmd.Flags().GetString("nfs-path")
		d := map[string]interface{}{
			"name":     name,
			"nfs_path": nfsPath,
		}
		result, err := cl.do("POST", "/api/datasets", d)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Created dataset %s (ID: %s)\n", str(result, "name"), str(result, "id"))
		return nil
	},
}

// ─── Get ─────────────────────────────────────────────────────────────────────

var datasetsGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get dataset details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/datasets/"+args[0], nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("ID:       %s\nName:     %s\nNFS Path: %s\nVersion:  %s\nSize:     %s bytes\n",
			str(result, "id"), str(result, "name"), str(result, "nfs_path"),
			str(result, "version"), str(result, "size_bytes"))
		return nil
	},
}

// ─── Scan ────────────────────────────────────────────────────────────────────

var datasetsScanCmd = &cobra.Command{
	Use:   "scan ID",
	Short: "Trigger an NFS scan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		_, err := cl.do("POST", "/api/datasets/"+args[0]+"/scan", nil)
		if err != nil {
			return err
		}
		fmt.Println("Scan triggered for dataset", args[0])
		return nil
	},
}

// ─── Delete ──────────────────────────────────────────────────────────────────

var datasetsDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		_, err := cl.do("DELETE", "/api/datasets/"+args[0], nil)
		if err != nil {
			return err
		}
		fmt.Println("Deleted dataset", args[0])
		return nil
	},
}
