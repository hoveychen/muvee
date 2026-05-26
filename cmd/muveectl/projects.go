package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// ─── Parent command ──────────────────────────────────────────────────────────

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage projects",
}

// resolveResourceRef accepts either a UUID (passed through unchanged) or a
// resource name and looks it up against the given list endpoint, matching
// on the `name` field. Returns the canonical UUID string, or an error
// shaped for CLI display ("<kind> not found" / "ambiguous <kind> name").
func resolveResourceRef(c *client, kind, listURL, arg string) (string, error) {
	if _, err := uuid.Parse(arg); err == nil {
		return arg, nil
	}
	items, err := c.doArray("GET", listURL, nil)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", kind, arg, err)
	}
	var matches []string
	for _, raw := range items {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if str(m, "name") == arg {
			matches = append(matches, str(m, "id"))
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s not found: %s", kind, arg)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous %s name: %s (matched %d; use the UUID instead)", kind, arg, len(matches))
	}
}

// resolveProjectRef accepts a project UUID or name (scoped to projects the
// caller can see via GET /api/projects).
func resolveProjectRef(c *client, arg string) (string, error) {
	return resolveResourceRef(c, "project", "/api/projects", arg)
}

// resolveDatasetRef accepts a dataset UUID or name (scoped to datasets the
// caller can see via GET /api/datasets).
func resolveDatasetRef(c *client, arg string) (string, error) {
	return resolveResourceRef(c, "dataset", "/api/datasets", arg)
}

func init() {
	rootCmd.AddCommand(projectsCmd)

	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsCreateCmd)
	projectsCmd.AddCommand(projectsGetCmd)
	projectsCmd.AddCommand(projectsUpdateCmd)
	projectsCmd.AddCommand(projectsDeleteCmd)
	projectsCmd.AddCommand(projectsDeployCmd)
	projectsCmd.AddCommand(projectsLatestImageCmd)
	projectsCmd.AddCommand(projectsDeploymentsCmd)
	projectsCmd.AddCommand(projectsLogsCmd)
	projectsCmd.AddCommand(projectsRuntimeLogsCmd)
	projectsCmd.AddCommand(projectsMetricsCmd)
	projectsCmd.AddCommand(projectsPortForwardCmd)
	projectsCmd.AddCommand(projectsCurlCmd)
	projectsCmd.AddCommand(projectsExecCmd)
	projectsCmd.AddCommand(projectsShellCmd)
	projectsCmd.AddCommand(projectsCpCmd)
	projectsCmd.AddCommand(projectsRestartCmd)
	projectsRestartCmd.Flags().Duration("timeout", 60*time.Second, "Max wait for the restart task to complete")
	projectsCmd.AddCommand(projectsEnvCmd)
	projectsEnvCmd.Flags().Bool("raw", false, "Show env values unmasked (still gated by project access)")
	projectsEnvCmd.Flags().Duration("timeout", 60*time.Second, "Max wait for the env task to complete")
	projectsCmd.AddCommand(projectsDescribeCmd)
	projectsDescribeCmd.Flags().String("output", "text", "Output format: text|json")
	projectsDescribeCmd.Flags().Duration("timeout", 60*time.Second, "Max wait for the describe task to complete")
	projectsCmd.AddCommand(projectsEventsCmd)
	projectsEventsCmd.Flags().BoolP("follow", "f", false, "Stream new events (poll every 3 s; Ctrl-C to stop)")
	projectsEventsCmd.Flags().Int("limit", 200, "Max number of events to return per poll (cap 200)")

	// projects create flags
	addProjectFlags(projectsCreateCmd)
	projectsCreateCmd.MarkFlagRequired("name")

	// projects update flags
	addProjectFlags(projectsUpdateCmd)
	projectsUpdateCmd.Flags().String("owner", "", "Admin only: reassign the project to this owner (username or UUID); applied via a separate admin endpoint")

	// projects logs flags
	projectsLogsCmd.Flags().String("deployment", "", "Specific deployment ID (defaults to latest)")

	// projects runtime-logs flags
	projectsRuntimeLogsCmd.Flags().Int("tail", 200, "Number of log lines to fetch (0 = no limit, capped at 1 MiB server-side)")
	projectsRuntimeLogsCmd.Flags().String("since", "", "Only return logs since this time (e.g. 1h, 10m, 2026-05-14T08:00:00)")
	projectsRuntimeLogsCmd.Flags().BoolP("follow", "f", false, "Stream new lines (poll every 5 s; Ctrl-C to stop)")
	projectsRuntimeLogsCmd.Flags().Duration("poll-timeout", 60*time.Second, "Max wait for a single snapshot before giving up")

	// projects metrics flags
	projectsMetricsCmd.Flags().Int("limit", 60, "Number of metric samples")

	// projects port-forward flags
	projectsPortForwardCmd.Flags().String("port", "0", "Local port (0 = auto-pick)")

	// projects curl flags
	projectsCurlCmd.Flags().StringP("method", "X", "GET", "HTTP method")
	projectsCurlCmd.Flags().StringP("data", "d", "", "Request body (string)")
	projectsCurlCmd.Flags().Bool("data-stdin", false, "Read request body from stdin (overrides --data)")
	projectsCurlCmd.Flags().StringArrayP("header", "H", nil, "Extra header 'Name: Value' (repeatable)")
	projectsCurlCmd.Flags().BoolP("include", "i", false, "Print response status line + headers before the body")
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
	cmd.Flags().Bool("auto-deploy", false, "Enable auto-redeploy when the tracked branch (or upstream image digest) changes")
	cmd.Flags().Bool("no-auto-deploy", false, "Disable auto-redeploy")
	cmd.Flags().String("auth-domains", "", "Comma-separated allowed email domains")
	cmd.Flags().String("auth-bypass-paths", "", "Newline-separated paths that bypass auth (use * suffix for prefix match, e.g. /api/public/*)")
	cmd.Flags().Bool("domain-only", false, "Reserve a tunnel domain prefix without a git repo (no deployment)")
	cmd.Flags().Bool("compose", false, "Deploy via docker-compose (images only, no build)")
	cmd.Flags().String("compose-file", "", "Compose file path relative to repo root (default: docker-compose.yml)")
	cmd.Flags().String("expose-service", "", "Compose service name to expose via the muvee router")
	cmd.Flags().Int("expose-port", 0, "Container port on the exposed service to publish")
	cmd.Flags().String("image-ref", "", "OCI image reference (e.g. ghcr.io/foo/bar:latest); presence selects the image-only project type")
	cmd.Flags().Bool("build-only", false, "Build + push a Docker image only (no container started, no host port, no route); downstream compose/image projects reference the resulting last_image_tag")
	cmd.Flags().String("triggers-redeploy-of", "", "Comma-separated downstream project IDs to auto-redeploy after a successful build push (build-only projects)")
	cmd.Flags().Int("container-port", 0, "Container port to publish (image-only project type; default 8080)")
	cmd.Flags().String("memory-limit", "", "Container memory limit (e.g. 4g, 512m)")
	cmd.Flags().String("volume-mount-path", "", "Container path for the persistent named volume (compose/image projects)")
	cmd.Flags().String("description", "", "Project description")
	cmd.Flags().String("icon", "", "Project icon (inline SVG or URL)")
	cmd.Flags().String("tags", "", "Comma-separated project tags")
	cmd.Flags().Int("fixed-port", 0, "Admin only: fix the published host port (1024-65535); requires --fixed-node")
	cmd.Flags().String("fixed-node", "", "Admin only: pin the project to a deployer node UUID; requires --fixed-port")
	cmd.Flags().Bool("clear-fixed-port", false, "Admin only: remove the fixed-port binding (clears both fixed_host_port and fixed_node_id)")
	// Sign-in page branding (rendered on the forward-auth login page when the project is private).
	cmd.Flags().String("branding-site-name", "", "Branding: headline shown on the sidebar and browser tab")
	cmd.Flags().String("branding-logo-url", "", "Branding: HTTPS logo URL (replaces site name on sidebar/header)")
	cmd.Flags().String("branding-favicon-url", "", "Branding: HTTPS favicon URL (.ico/.png/.svg)")
	cmd.Flags().String("branding-primary-color", "", "Branding: primary hex colour, e.g. #4f46e5")
	cmd.Flags().String("branding-sidebar-bg", "", "Branding: sidebar background hex colour")
	cmd.Flags().String("branding-tagline", "", "Branding: small uppercase tagline at top of sidebar")
	cmd.Flags().String("branding-description", "", "Branding: multi-line description shown under the brand")
	cmd.Flags().String("branding-footer-text", "", "Branding: single-line footer text (empty hides the footer)")
	cmd.Flags().String("branding-trust-text", "", "Branding: comma-separated trust badges (up to 3, e.g. 'Encrypted,SOC 2,GDPR')")
}

func resolveUserRef(c *client, arg string) (string, error) {
	return resolveResourceRef(c, "user", "/api/users", arg)
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
	if cmd.Flags().Changed("auto-deploy") {
		p["auto_deploy_enabled"] = true
	}
	if cmd.Flags().Changed("no-auto-deploy") {
		p["auto_deploy_enabled"] = false
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
	if cmd.Flags().Changed("image-ref") {
		v, _ := cmd.Flags().GetString("image-ref")
		p["image_ref"] = v
	}
	if cmd.Flags().Changed("build-only") {
		if v, _ := cmd.Flags().GetBool("build-only"); v {
			p["project_type"] = "build"
		}
	}
	if cmd.Flags().Changed("triggers-redeploy-of") {
		v, _ := cmd.Flags().GetString("triggers-redeploy-of")
		ids := []string{}
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				ids = append(ids, part)
			}
		}
		// Encode as JSON array string so it round-trips through the existing
		// triggers_redeploy_of TEXT column.
		b, _ := json.Marshal(ids)
		p["triggers_redeploy_of"] = string(b)
	}
	if cmd.Flags().Changed("container-port") {
		v, _ := cmd.Flags().GetInt("container-port")
		p["container_port"] = v
	}
	if cmd.Flags().Changed("memory-limit") {
		v, _ := cmd.Flags().GetString("memory-limit")
		p["memory_limit"] = v
	}
	if cmd.Flags().Changed("volume-mount-path") {
		v, _ := cmd.Flags().GetString("volume-mount-path")
		p["volume_mount_path"] = v
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
	if v, _ := cmd.Flags().GetBool("clear-fixed-port"); v {
		p["fixed_host_port"] = nil
		p["fixed_node_id"] = nil
	} else {
		if cmd.Flags().Changed("fixed-port") {
			v, _ := cmd.Flags().GetInt("fixed-port")
			p["fixed_host_port"] = v
		}
		if cmd.Flags().Changed("fixed-node") {
			v, _ := cmd.Flags().GetString("fixed-node")
			p["fixed_node_id"] = v
		}
	}
	for _, m := range []struct{ flag, field string }{
		{"branding-site-name", "branding_site_name"},
		{"branding-logo-url", "branding_logo_url"},
		{"branding-favicon-url", "branding_favicon_url"},
		{"branding-primary-color", "branding_primary_color"},
		{"branding-sidebar-bg", "branding_sidebar_bg"},
		{"branding-tagline", "branding_tagline"},
		{"branding-description", "branding_description"},
		{"branding-footer-text", "branding_footer_text"},
		{"branding-trust-text", "branding_trust_text"},
	} {
		if cmd.Flags().Changed(m.flag) {
			v, _ := cmd.Flags().GetString(m.flag)
			p[m.field] = v
		}
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
		imageMode := cmd.Flags().Changed("image-ref")
		buildOnly, _ := cmd.Flags().GetBool("build-only")
		isHosted, _ := cmd.Flags().GetString("git-source")
		if domainOnly && composeMode {
			return fmt.Errorf("--domain-only and --compose are mutually exclusive")
		}
		if imageMode && (domainOnly || composeMode) {
			return fmt.Errorf("--image-ref is mutually exclusive with --domain-only / --compose")
		}
		if buildOnly && (domainOnly || composeMode || imageMode) {
			return fmt.Errorf("--build-only is mutually exclusive with --domain-only / --compose / --image-ref")
		}
		if imageMode {
			if cmd.Flags().Changed("git-url") || cmd.Flags().Changed("git-source") {
				return fmt.Errorf("--git-url and --git-source are not allowed with --image-ref")
			}
			p["project_type"] = "image"
		} else if composeMode {
			if isHosted != "hosted" && !cmd.Flags().Changed("git-url") {
				return fmt.Errorf("--git-url is required for compose projects (or use --git-source hosted)")
			}
			if !cmd.Flags().Changed("expose-service") {
				return fmt.Errorf("--expose-service is required for compose projects")
			}
			if !cmd.Flags().Changed("expose-port") {
				return fmt.Errorf("--expose-port is required for compose projects")
			}
		} else if buildOnly {
			if isHosted != "hosted" && !cmd.Flags().Changed("git-url") {
				return fmt.Errorf("--git-url is required for build-only projects (or use --git-source hosted)")
			}
			for _, bad := range []string{"container-port", "expose-port", "expose-service", "auth-required", "memory-limit", "volume-mount-path", "fixed-port", "fixed-node"} {
				if cmd.Flags().Changed(bad) {
					return fmt.Errorf("--%s is not allowed with --build-only (build projects don't serve traffic)", bad)
				}
			}
		} else if domainOnly {
			if !cmd.Flags().Changed("domain") {
				return fmt.Errorf("--domain is required when --domain-only is set")
			}
			if cmd.Flags().Changed("git-url") || cmd.Flags().Changed("git-source") {
				return fmt.Errorf("--git-url and --git-source are not allowed with --domain-only")
			}
		} else if isHosted != "hosted" && !cmd.Flags().Changed("git-url") {
			return fmt.Errorf("--git-url is required (or use --git-source hosted, --domain-only, --compose, --image-ref, or --build-only)")
		}
		if cmd.Flags().Changed("fixed-port") != cmd.Flags().Changed("fixed-node") {
			return fmt.Errorf("--fixed-port and --fixed-node must be set together")
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
	Use:   "get ID-OR-NAME",
	Short: "Get project details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/projects/"+id, nil)
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
		if tag := str(result, "last_image_tag"); tag != "" {
			fmt.Printf("Last Image:    %s\n", tag)
		}
		if trig := str(result, "triggers_redeploy_of"); trig != "" && trig != "[]" {
			fmt.Printf("Auto-chain:    %s\n", trig)
		}
		return nil
	},
}

// ─── Latest Image ────────────────────────────────────────────────────────────

// projectsLatestImageCmd prints `last_image_tag` for a build-only project in a
// shape suitable for shell pipelines, e.g.
//
//	HUB_IMAGE=$(muveectl projects latest-image hub-builder)
var projectsLatestImageCmd = &cobra.Command{
	Use:   "latest-image ID-OR-NAME",
	Short: "Print the last image tag built by a build-only project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		result, err := cl.do("GET", "/api/projects/"+id, nil)
		if err != nil {
			return err
		}
		tag := str(result, "last_image_tag")
		if tag == "" {
			return fmt.Errorf("project %s has no last_image_tag yet (deploy it at least once)", args[0])
		}
		fmt.Println(tag)
		return nil
	},
}

// ─── Update ──────────────────────────────────────────────────────────────────

var projectsUpdateCmd = &cobra.Command{
	Use:   "update ID-OR-NAME",
	Short: "Update project configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		p := collectProjectFlags(cmd)
		var result map[string]interface{}
		if len(p) > 0 {
			result, err = cl.do("PUT", "/api/projects/"+id, p)
			if err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("owner") {
			ref, _ := cmd.Flags().GetString("owner")
			ownerID, err := resolveUserRef(cl, ref)
			if err != nil {
				return fmt.Errorf("resolve --owner: %w", err)
			}
			result, err = cl.do("PUT", "/api/projects/"+id+"/owner", map[string]interface{}{"owner_id": ownerID})
			if err != nil {
				return err
			}
		}
		if result == nil {
			return fmt.Errorf("nothing to update — pass at least one flag")
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
	Use:   "delete ID-OR-NAME",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		if _, err := cl.do("DELETE", "/api/projects/"+id, nil); err != nil {
			return err
		}
		fmt.Println("Deleted project", args[0])
		return nil
	},
}

// ─── Deploy ──────────────────────────────────────────────────────────────────

var projectsDeployCmd = &cobra.Command{
	Use:   "deploy ID-OR-NAME",
	Short: "Trigger a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		result, err := cl.do("POST", "/api/projects/"+id+"/deploy", nil)
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
	Use:   "deployments ID-OR-NAME",
	Short: "List deployment history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects/"+id+"/deployments", nil)
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
	Use:   "logs ID-OR-NAME",
	Short: "Show build/deploy logs (latest deployment by default)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		deploymentID, _ := cmd.Flags().GetString("deployment")

		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		items, err := cl.doArray("GET", "/api/projects/"+id+"/deployments", nil)
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

// ─── Runtime logs ────────────────────────────────────────────────────────────

var projectsRuntimeLogsCmd = &cobra.Command{
	Use:   "runtime-logs ID-OR-NAME",
	Short: "Show container stdout/stderr from the running deployment (for crash / restart debugging)",
	Long: `Fetch the live container stdout/stderr from the deploy node where the
project's current deployment is running. Unlike "projects logs" (which shows
the build/deploy phase output captured during ` + "`docker build`" + ` / ` + "`docker run`" + `),
this command runs ` + "`docker logs`" + ` against the container itself, so it surfaces
panics, restart reasons, and runtime errors.

Requires a running deployment on a deploy node; returns 409 otherwise.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		tail, _ := cmd.Flags().GetInt("tail")
		since, _ := cmd.Flags().GetString("since")
		follow, _ := cmd.Flags().GetBool("follow")
		pollTimeout, _ := cmd.Flags().GetDuration("poll-timeout")

		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}

		if !follow {
			out, err := fetchRuntimeLogsSnapshot(cl, id, tail, since, pollTimeout)
			if err != nil {
				return err
			}
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
			return nil
		}

		// Follow mode: print initial snapshot, then loop polling for new lines.
		// We advance the "since" cursor by walking it forward each iteration.
		// Docker accepts both RFC3339 timestamps and relative durations; we use
		// RFC3339 (UTC) once we have an anchor so subsequent calls don't re-fetch
		// the whole window each time.
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		initialSince := since
		if initialSince == "" {
			initialSince = "1h"
		}
		out, err := fetchRuntimeLogsSnapshot(cl, id, tail, initialSince, pollTimeout)
		if err != nil {
			return err
		}
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}

		// Subsequent polls use an RFC3339 timestamp anchored at "now" before
		// each request. This intentionally trades a small overlap risk (lines
		// emitted between the agent's docker-logs call and our cursor update
		// could be skipped) for not re-printing the entire window. For a debug
		// tool this is acceptable; users who need full fidelity can rerun
		// without --follow.
		cursor := time.Now().UTC().Format(time.RFC3339)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				nextCursor := time.Now().UTC().Format(time.RFC3339)
				chunk, err := fetchRuntimeLogsSnapshot(cl, id, 0, cursor, pollTimeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "poll error: %v\n", err)
					continue
				}
				if strings.TrimSpace(chunk) != "" {
					fmt.Print(chunk)
					if !strings.HasSuffix(chunk, "\n") {
						fmt.Println()
					}
				}
				cursor = nextCursor
			}
		}
	},
}

// fetchRuntimeLogsSnapshot dispatches a runtime_logs task to the control plane
// and polls /api/tasks/{id} until it completes or pollTimeout elapses.
func fetchRuntimeLogsSnapshot(c *client, projectID string, tail int, since string, pollTimeout time.Duration) (string, error) {
	q := url.Values{}
	if tail > 0 {
		q.Set("tail", fmt.Sprintf("%d", tail))
	}
	if since != "" {
		q.Set("since", since)
	}
	path := "/api/projects/" + projectID + "/runtime-logs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.do("POST", path, nil)
	if err != nil {
		return "", err
	}
	taskID := str(resp, "task_id")
	if taskID == "" {
		return "", fmt.Errorf("server did not return task_id")
	}

	deadline := time.Now().Add(pollTimeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for runtime logs task %s", taskID)
		}
		task, err := c.do("GET", "/api/tasks/"+taskID, nil)
		if err != nil {
			return "", err
		}
		switch str(task, "status") {
		case "completed":
			return str(task, "result"), nil
		case "failed":
			result := str(task, "result")
			if result == "" {
				result = "(no error message)"
			}
			return "", fmt.Errorf("runtime logs task failed: %s", result)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ─── Metrics ─────────────────────────────────────────────────────────────────

var projectsMetricsCmd = &cobra.Command{
	Use:   "metrics ID-OR-NAME",
	Short: "Show container resource metrics (CPU, mem, net, disk)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		id, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		items, err := cl.doArray("GET", fmt.Sprintf("/api/projects/%s/metrics?limit=%d", id, limit), nil)
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
	Use:   "port-forward ID-OR-NAME",
	Short: "Forward a local port to the project's running container",
	Long:  "Auth is injected automatically using your CLI identity.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		localPort, _ := cmd.Flags().GetString("port")
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}

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

// ─── Curl ────────────────────────────────────────────────────────────────────

var projectsCurlCmd = &cobra.Command{
	Use:   "curl ID-OR-NAME [PATH]",
	Short: "Send a single HTTP request to the project's running container as yourself",
	Long: `Sends a single HTTP request to the project's running deployment via the
authenticated proxy endpoint, using your CLI identity. Bypasses Traefik
ForwardAuth — auth-required and access_mode=private projects work as long
as you are an owner / member / admin of the project.

The request goes to <server>/api/projects/<id>/proxy<path>. The container
sees X-Forwarded-User: <your email> just like it would through Traefik.

Examples:
  muveectl projects curl <id> /healthz
  muveectl projects curl <id> /api/users -X POST -H 'Content-Type: application/json' -d '{"name":"x"}'
  cat payload.json | muveectl projects curl <id> /api/upload -X POST --data-stdin -i`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		path := "/"
		if len(args) == 2 {
			path = args[1]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
		}

		method, _ := cmd.Flags().GetString("method")
		data, _ := cmd.Flags().GetString("data")
		headers, _ := cmd.Flags().GetStringArray("header")
		include, _ := cmd.Flags().GetBool("include")
		dataStdin, _ := cmd.Flags().GetBool("data-stdin")

		var body io.Reader
		switch {
		case dataStdin:
			buf, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			body = bytes.NewReader(buf)
		case data != "":
			body = strings.NewReader(data)
		}

		targetURL := cl.server + "/api/projects/" + projectID + "/proxy" + path
		req, err := http.NewRequest(strings.ToUpper(method), targetURL, body)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		for _, h := range headers {
			parts := strings.SplitN(h, ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("bad header %q (expected 'Name: Value')", h)
			}
			req.Header.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
		req.Header.Set("Authorization", "Bearer "+cl.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("request: %w", err)
		}
		defer resp.Body.Close()

		if include {
			fmt.Fprintf(os.Stdout, "%s %s\n", resp.Proto, resp.Status)
			resp.Header.Write(os.Stdout)
			fmt.Fprintln(os.Stdout)
		}
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode >= 400 {
			os.Exit(1)
		}
		return nil
	},
}

