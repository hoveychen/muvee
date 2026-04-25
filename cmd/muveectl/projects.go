package main

import (
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage projects",
}

func init() {
	rootCmd.AddCommand(projectsCmd)

	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsCreateCmd)
	projectsCmd.AddCommand(projectsGetCmd)
	projectsCmd.AddCommand(projectsUpdateCmd)
	projectsCmd.AddCommand(projectsDeleteCmd)
	projectsCmd.AddCommand(projectsDeployCmd)
	projectsCmd.AddCommand(projectsDeploymentsCmd)
	projectsCmd.AddCommand(projectsLogsCmd)
	projectsCmd.AddCommand(projectsMetricsCmd)
	projectsCmd.AddCommand(projectsPortForwardCmd)

	// projects create flags
	addProjectFlags(projectsCreateCmd)
	projectsCreateCmd.MarkFlagRequired("name")

	// projects update flags
	addProjectFlags(projectsUpdateCmd)

	// projects logs flags
	projectsLogsCmd.Flags().String("deployment", "", "Specific deployment ID (defaults to latest)")

	// projects metrics flags
	projectsMetricsCmd.Flags().Int("limit", 60, "Number of metric samples")

	// projects port-forward flags
	projectsPortForwardCmd.Flags().String("port", "0", "Local port (0 = auto-pick)")
}

func addProjectFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "Project name")
	cmd.Flags().String("git-url", "", "Git repository URL (required unless --git-source hosted or --domain-only)")
	cmd.Flags().String("git-source", "", "Git source (use 'hosted' for server-hosted repo)")
	cmd.Flags().String("branch", "", "Git branch (default: main)")
	cmd.Flags().String("domain", "", "Domain prefix (defaults to project name)")
	cmd.Flags().String("dockerfile", "", "Dockerfile path relative to repo root (default: Dockerfile)")
	cmd.Flags().Bool("auth-required", false, "Enable OAuth protection via Traefik ForwardAuth")
	cmd.Flags().Bool("no-auth", false, "Disable OAuth protection")
	cmd.Flags().String("auth-domains", "", "Comma-separated allowed email domains")
	cmd.Flags().String("auth-bypass-paths", "", "Newline-separated paths that bypass auth (use * suffix for prefix match, e.g. /api/public/*)")
	cmd.Flags().Bool("domain-only", false, "Reserve a tunnel domain prefix without a git repo (no deployment)")
	cmd.Flags().Bool("compose", false, "Deploy via docker-compose (images only, no build)")
	cmd.Flags().String("compose-file", "", "Compose file path relative to repo root (default: docker-compose.yml)")
	cmd.Flags().String("expose-service", "", "Compose service name to expose via the muvee router")
	cmd.Flags().Int("expose-port", 0, "Container port on the exposed service to publish")
	cmd.Flags().String("description", "", "Project description")
	cmd.Flags().String("icon", "", "Project icon (inline SVG or URL)")
	cmd.Flags().String("tags", "", "Comma-separated project tags")
}

func collectProjectFlags(cmd *cobra.Command) map[string]interface{} {
	p := map[string]interface{}{}
	if cmd.Flags().Changed("name") {
		v, _ := cmd.Flags().GetString("name")
		p["name"] = v
	}
	if cmd.Flags().Changed("git-url") {
		v, _ := cmd.Flags().GetString("git-url")
		p["git_url"] = v
	}
	if cmd.Flags().Changed("git-source") {
		v, _ := cmd.Flags().GetString("git-source")
		p["git_source"] = v
	}
	if cmd.Flags().Changed("branch") {
		v, _ := cmd.Flags().GetString("branch")
		p["git_branch"] = v
	}
	if cmd.Flags().Changed("domain") {
		v, _ := cmd.Flags().GetString("domain")
		p["domain_prefix"] = v
	}
	if cmd.Flags().Changed("dockerfile") {
		v, _ := cmd.Flags().GetString("dockerfile")
		p["dockerfile_path"] = v
	}
	if cmd.Flags().Changed("auth-required") {
		p["auth_required"] = true
	}
	if cmd.Flags().Changed("no-auth") {
		p["auth_required"] = false
	}
	if cmd.Flags().Changed("auth-domains") {
		v, _ := cmd.Flags().GetString("auth-domains")
		p["auth_allowed_domains"] = v
	}
	if cmd.Flags().Changed("auth-bypass-paths") {
		v, _ := cmd.Flags().GetString("auth-bypass-paths")
		p["auth_bypass_paths"] = v
	}
	if cmd.Flags().Changed("domain-only") {
		if v, _ := cmd.Flags().GetBool("domain-only"); v {
			p["project_type"] = "domain_only"
		}
	}
	if cmd.Flags().Changed("compose") {
		if v, _ := cmd.Flags().GetBool("compose"); v {
			p["project_type"] = "compose"
		}
	}
	if cmd.Flags().Changed("compose-file") {
		v, _ := cmd.Flags().GetString("compose-file")
		p["compose_file_path"] = v
	}
	if cmd.Flags().Changed("expose-service") {
		v, _ := cmd.Flags().GetString("expose-service")
		p["expose_service"] = v
	}
	if cmd.Flags().Changed("expose-port") {
		v, _ := cmd.Flags().GetInt("expose-port")
		p["expose_port"] = v
	}
	if cmd.Flags().Changed("description") {
		v, _ := cmd.Flags().GetString("description")
		p["description"] = v
	}
	if cmd.Flags().Changed("icon") {
		v, _ := cmd.Flags().GetString("icon")
		p["icon"] = v
	}
	if cmd.Flags().Changed("tags") {
		v, _ := cmd.Flags().GetString("tags")
		p["tags"] = v
	}
	return p
}

// ─── List ────────────────────────────────────────────────────────────────────

var projectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No projects found.")
			return nil
		}
		printTable(items, []string{"id", "name", "project_type", "domain_prefix", "git_branch"})
		return nil
	},
}

// ─── Create ──────────────────────────────────────────────────────────────────

var projectsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		p := collectProjectFlags(cmd)
		domainOnly, _ := cmd.Flags().GetBool("domain-only")
		composeMode, _ := cmd.Flags().GetBool("compose")
		isHosted, _ := cmd.Flags().GetString("git-source")
		if domainOnly && composeMode {
			return fmt.Errorf("--domain-only and --compose are mutually exclusive")
		}
		if composeMode {
			if !cmd.Flags().Changed("git-url") {
				return fmt.Errorf("--git-url is required for compose projects")
			}
			if isHosted == "hosted" {
				return fmt.Errorf("compose projects must use an external git repository")
			}
			if !cmd.Flags().Changed("expose-service") {
				return fmt.Errorf("--expose-service is required for compose projects")
			}
			if !cmd.Flags().Changed("expose-port") {
				return fmt.Errorf("--expose-port is required for compose projects")
			}
		} else if domainOnly {
			if !cmd.Flags().Changed("domain") {
				return fmt.Errorf("--domain is required when --domain-only is set")
			}
			if cmd.Flags().Changed("git-url") || cmd.Flags().Changed("git-source") {
				return fmt.Errorf("--git-url and --git-source are not allowed with --domain-only")
			}
		} else if isHosted != "hosted" && !cmd.Flags().Changed("git-url") {
			return fmt.Errorf("--git-url is required (or use --git-source hosted, --domain-only, or --compose)")
		}
		result, err := cl.do("POST", "/api/projects", p)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Created project %s (ID: %s)\n", str(result, "name"), str(result, "id"))
		if pushURL := str(result, "git_push_url"); pushURL != "" {
			fmt.Printf("Git Push URL:  %s\n", pushURL)
			fmt.Printf("\nPush your code:\n  git remote add muvee %s\n  git push muvee main\n", pushURL)
			fmt.Println("\nUse any username and your API token as the password.")
		}
		return nil
	},
}

// ─── Get ─────────────────────────────────────────────────────────────────────

var projectsGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get project details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/projects/"+args[0], nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("ID:            %s\nName:          %s\nType:          %s\nGit Source:    %s\nGit URL:       %s\nBranch:        %s\nDomain Prefix: %s\nDockerfile:    %s\nDescription:   %s\nIcon:          %s\nTags:          %s\n",
			str(result, "id"), str(result, "name"), str(result, "project_type"), str(result, "git_source"), str(result, "git_url"), str(result, "git_branch"),
			str(result, "domain_prefix"), str(result, "dockerfile_path"),
			str(result, "description"), str(result, "icon"), str(result, "tags"))
		if pushURL := str(result, "git_push_url"); pushURL != "" {
			fmt.Printf("Git Push URL:  %s\n", pushURL)
		}
		return nil
	},
}

// ─── Update ──────────────────────────────────────────────────────────────────

var projectsUpdateCmd = &cobra.Command{
	Use:   "update ID",
	Short: "Update project configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		p := collectProjectFlags(cmd)
		result, err := cl.do("PUT", "/api/projects/"+args[0], p)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Updated project %s\n", str(result, "name"))
		return nil
	},
}

// ─── Delete ──────────────────────────────────────────────────────────────────

var projectsDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		_, err := cl.do("DELETE", "/api/projects/"+args[0], nil)
		if err != nil {
			return err
		}
		fmt.Println("Deleted project", args[0])
		return nil
	},
}

// ─── Deploy ──────────────────────────────────────────────────────────────────

var projectsDeployCmd = &cobra.Command{
	Use:   "deploy ID",
	Short: "Trigger a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		result, err := cl.do("POST", "/api/projects/"+args[0]+"/deploy", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(result)
			return nil
		}
		fmt.Printf("Deployment triggered (ID: %s, status: %s)\n", str(result, "id"), str(result, "status"))
		return nil
	},
}

// ─── Deployments ─────────────────────────────────────────────────────────────

var projectsDeploymentsCmd = &cobra.Command{
	Use:   "deployments ID",
	Short: "List deployment history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects/"+args[0]+"/deployments", nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No deployments found.")
			return nil
		}
		printTable(items, []string{"id", "status", "commit_sha", "updated_at"})
		return nil
	},
}

// ─── Logs ────────────────────────────────────────────────────────────────────

var projectsLogsCmd = &cobra.Command{
	Use:   "logs ID",
	Short: "Show build/deploy logs (latest deployment by default)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		deploymentID, _ := cmd.Flags().GetString("deployment")

		items, err := cl.doArray("GET", "/api/projects/"+args[0]+"/deployments", nil)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No deployments found.")
			return nil
		}

		if deploymentID == "" {
			deploymentID = str(items[0].(map[string]interface{}), "id")
		}

		// Find the deployment in the list to get its logs.
		var deployment map[string]interface{}
		for _, d := range items {
			dm := d.(map[string]interface{})
			if str(dm, "id") == deploymentID {
				deployment = dm
				break
			}
		}
		if deployment == nil {
			return fmt.Errorf("deployment %s not found", deploymentID)
		}

		if jsonMode {
			printJSON(deployment)
			return nil
		}

		fmt.Printf("Deployment: %s  Status: %s  Commit: %s\n",
			str(deployment, "id"), str(deployment, "status"), str(deployment, "commit_sha"))
		fmt.Println("---")
		logs := str(deployment, "logs")
		if logs == "" {
			fmt.Println("(no logs)")
		} else {
			fmt.Print(logs)
		}
		return nil
	},
}

// ─── Metrics ─────────────────────────────────────────────────────────────────

var projectsMetricsCmd = &cobra.Command{
	Use:   "metrics ID",
	Short: "Show container resource metrics (CPU, mem, net, disk)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		items, err := cl.doArray("GET", fmt.Sprintf("/api/projects/%s/metrics?limit=%d", args[0], limit), nil)
		if err != nil {
			return err
		}
		if jsonMode {
			printJSON(items)
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No metrics available. The container may not be running or metrics have not been collected yet.")
			return nil
		}
		// Pretty-print the latest sample at the top, then a compact table.
		latest, _ := items[0].(map[string]interface{})
		if latest != nil {
			fmt.Printf("Latest sample (collected_at: %s)\n", str(latest, "collected_at"))
			fmt.Printf("  CPU:        %s%%\n", str(latest, "cpu_percent"))
			memUsage := floatStr(latest, "mem_usage_bytes")
			memLimit := floatStr(latest, "mem_limit_bytes")
			fmt.Printf("  Memory:     %s / %s bytes\n", memUsage, memLimit)
			fmt.Printf("  Net Rx:     %s bytes  Tx: %s bytes\n",
				str(latest, "net_rx_bytes"), str(latest, "net_tx_bytes"))
			fmt.Printf("  Disk Read:  %s bytes  Write: %s bytes\n",
				str(latest, "block_read_bytes"), str(latest, "block_write_bytes"))
			fmt.Println()
		}
		if len(items) > 1 {
			fmt.Printf("History (%d samples):\n", len(items))
			printTable(items, []string{"collected_at", "cpu_percent", "mem_usage_bytes", "net_rx_bytes", "net_tx_bytes"})
		}
		return nil
	},
}

// ─── Port Forward ────────────────────────────────────────────────────────────

var projectsPortForwardCmd = &cobra.Command{
	Use:   "port-forward ID",
	Short: "Forward a local port to the project's running container",
	Long:  "Auth is injected automatically using your CLI identity.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		localPort, _ := cmd.Flags().GetString("port")
		projectID := args[0]

		// Verify project exists and has a running deployment.
		proj, err := cl.do("GET", "/api/projects/"+projectID, nil)
		if err != nil {
			return fmt.Errorf("fetch project: %w", err)
		}

		proxyBase := cl.server + "/api/projects/" + projectID + "/proxy"

		ln, err := net.Listen("tcp", "127.0.0.1:"+localPort)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer ln.Close()

		fmt.Printf("Forwarding 127.0.0.1:%d → %s (project: %s)\n",
			ln.Addr().(*net.TCPAddr).Port, str(proj, "domain_prefix")+"."+str(proj, "name"), projectID)
		fmt.Println("Press Ctrl+C to stop.")

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetURL := proxyBase + r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			for k, vv := range r.Header {
				for _, v := range vv {
					proxyReq.Header.Add(k, v)
				}
			}
			proxyReq.Header.Set("Authorization", "Bearer "+cl.token)

			resp, err := http.DefaultClient.Do(proxyReq)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()

			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})

		return http.Serve(ln, handler)
	},
}

