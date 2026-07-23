package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

type Scheduler struct {
	store       *store.Store
	agentSecret string // shared secret for agent auth to hosted git repos
	// Image watcher config — populated by SetImageWatchConfig at startup.
	gitRepoBasePath  string
	registryAddr     string
	registryUser     string
	registryPassword string
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{store: st}
}

// SetGitHostingConfig configures the scheduler for hosted git repo builds.
func (s *Scheduler) SetGitHostingConfig(agentSecret string) {
	s.agentSecret = agentSecret
}

// SetImageWatchConfig wires the values the compose image-digest watcher needs.
// gitRepoBasePath is the on-disk root for hosted bare repos (used to read the
// docker-compose.yml at the tracked branch's tip without checking out a tree).
// registryAddr/User/Password are muvee's own private-registry credentials
// (already distributed to agents); the watcher reuses them when an image's
// host matches registryAddr, and falls back to anonymous auth otherwise.
func (s *Scheduler) SetImageWatchConfig(gitRepoBasePath, registryAddr, registryUser, registryPassword string) {
	s.gitRepoBasePath = gitRepoBasePath
	s.registryAddr = registryAddr
	s.registryUser = registryUser
	s.registryPassword = registryPassword
}

type nodeScore struct {
	node           *store.Node
	score          float64
	missingBytes   int64
	cachedDatasets map[uuid.UUID]bool
}

// PickDeployNode selects the best deploy node for a set of dependency datasets.
// Weights: W1=10 (cache hit), W2=0.001 (missing bytes), W3=0.0001 (free storage), W4=5 (container count, approximated by tasks)
func (s *Scheduler) PickDeployNode(ctx context.Context, datasetIDs []uuid.UUID) (*store.Node, error) {
	nodes, err := s.store.GetDeployNodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no deploy nodes available")
	}

	// Filter out nodes that haven't been seen in the last 2 minutes (offline)
	var activeNodes []*store.Node
	for _, n := range nodes {
		if time.Since(n.LastSeenAt) < 2*time.Minute {
			activeNodes = append(activeNodes, n)
		}
	}
	if len(activeNodes) == 0 {
		return nil, fmt.Errorf("no active deploy nodes")
	}

	// Get dataset sizes
	datasetSizes := make(map[uuid.UUID]int64)
	for _, id := range datasetIDs {
		d, err := s.store.GetDataset(ctx, id)
		if err != nil || d == nil {
			continue
		}
		datasetSizes[id] = d.SizeBytes
	}

	var scores []nodeScore
	for _, node := range activeNodes {
		nodeDSs, err := s.store.GetNodeDatasets(ctx, node.ID)
		if err != nil {
			continue
		}
		cached := make(map[uuid.UUID]bool)
		for _, nd := range nodeDSs {
			cached[nd.DatasetID] = true
		}

		var cacheHits int
		var missingBytes int64
		for _, id := range datasetIDs {
			if cached[id] {
				cacheHits++
			} else {
				missingBytes += datasetSizes[id]
			}
		}
		freeBytes := node.MaxStorageBytes - node.UsedStorageBytes
		score := float64(cacheHits)*10.0 - float64(missingBytes)*0.000001 + float64(freeBytes)*0.0000001
		scores = append(scores, nodeScore{
			node:           node,
			score:          score,
			missingBytes:   missingBytes,
			cachedDatasets: cached,
		})
	}
	if len(scores) == 0 {
		return nil, fmt.Errorf("no scorable nodes")
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	best := scores[0]

	// Check if we need to evict to free space
	freeBytes := best.node.MaxStorageBytes - best.node.UsedStorageBytes
	if best.missingBytes > freeBytes {
		needed := best.missingBytes - freeBytes
		evictList, err := s.store.GetLRUDatasetsForNode(ctx, best.node.ID, needed)
		if err != nil {
			return nil, fmt.Errorf("get LRU datasets: %w", err)
		}
		for _, nd := range evictList {
			if err := s.store.RemoveNodeDataset(ctx, nd.NodeID, nd.DatasetID); err != nil {
				return nil, err
			}
		}
	}
	return best.node, nil
}

// PickBuilderNode returns any active builder node.
func (s *Scheduler) PickBuilderNode(ctx context.Context) (*store.Node, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Role == store.NodeRoleBuilder && time.Since(n.LastSeenAt) < 2*time.Minute {
			return n, nil
		}
	}
	return nil, fmt.Errorf("no active builder nodes")
}

// DispatchCleanup sends a cleanup task to a specific node to remove a stale container.
// This is used when a deployment migrates to a different node and the old container
// on the previous node must be removed.
func (s *Scheduler) DispatchCleanup(ctx context.Context, nodeID uuid.UUID, stoppedDeployment *store.Deployment, domainPrefix string) error {
	task := &store.Task{
		Type:         store.TaskTypeCleanup,
		NodeID:       &nodeID,
		DeploymentID: stoppedDeployment.ID,
		Payload: map[string]interface{}{
			"domain_prefix": domainPrefix,
		},
	}
	_, err := s.store.CreateTask(ctx, task)
	return err
}

// DispatchRuntimeLogs creates a runtime_logs task on the node where the given
// deployment is running. The agent will run `docker logs --tail N --since T`
// against the deployment's container and write the captured output back into
// the task's Result field via the existing /api/agent/tasks/{id}/complete path.
// Returns the new task ID so the CLI can poll for completion.
//
// tail < 1 is treated as "no tail limit" (the agent will still cap the output
// size to avoid blowing up the DB column). since may be empty.
func (s *Scheduler) DispatchRuntimeLogs(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string, tail int, since string) (uuid.UUID, error) {
	payload := map[string]interface{}{
		"domain_prefix": domainPrefix,
	}
	if tail > 0 {
		payload["tail"] = tail
	}
	if since != "" {
		payload["since"] = since
	}
	task := &store.Task{
		Type:         store.TaskTypeRuntimeLogs,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload:      payload,
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchRestart creates a restart task on the node where the deployment is
// running. The agent runs `docker restart muvee-<domain_prefix>` and the CLI
// polls /api/tasks/{id} for completion.
func (s *Scheduler) DispatchRestart(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string) (uuid.UUID, error) {
	task := &store.Task{
		Type:         store.TaskTypeRestart,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload: map[string]interface{}{
			"domain_prefix": domainPrefix,
		},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchPause creates a pause task on the running deployment's node. The
// agent docker-stops every container carrying the project's domain_prefix
// label. The deployment row keeps its status so the node stays locatable for a
// later unpause.
func (s *Scheduler) DispatchPause(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string) (uuid.UUID, error) {
	task := &store.Task{
		Type:         store.TaskTypePause,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload:      map[string]interface{}{"domain_prefix": domainPrefix},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchUnpause creates an unpause task on the paused deployment's node. The
// agent docker-starts the project's stopped containers — no rebuild, no
// re-pull.
func (s *Scheduler) DispatchUnpause(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string) (uuid.UUID, error) {
	task := &store.Task{
		Type:         store.TaskTypeUnpause,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload:      map[string]interface{}{"domain_prefix": domainPrefix},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchEnv creates an env task on the running deployment's node. The agent
// inspects the container and returns its environment variables.
func (s *Scheduler) DispatchEnv(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string) (uuid.UUID, error) {
	task := &store.Task{
		Type:         store.TaskTypeEnv,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload:      map[string]interface{}{"domain_prefix": domainPrefix},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchDescribe creates a describe task on the running deployment's node.
// The agent collects container state (status, exit code, OOMKilled, restart
// count, image+sha, ports, env summary, mounts) via `docker inspect`.
func (s *Scheduler) DispatchDescribe(ctx context.Context, nodeID uuid.UUID, deployment *store.Deployment, domainPrefix string) (uuid.UUID, error) {
	task := &store.Task{
		Type:         store.TaskTypeDescribe,
		NodeID:       &nodeID,
		DeploymentID: deployment.ID,
		Payload:      map[string]interface{}{"domain_prefix": domainPrefix},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// DispatchComposeCleanup tears down a compose project on its pinned node,
// including its docker named volumes. Used when a compose project is deleted.
// deploymentID ties the task to a deployment row for FK purposes; the caller is
// responsible for keeping that deployment alive until the task reaches a
// terminal state (see deleteProject, which waits synchronously before deleting
// the project — otherwise the ON DELETE CASCADE would drop this task before the
// agent ever claims it). Returns the created task's ID so the caller can poll
// for completion; returns uuid.Nil when the project was never deployed.
func (s *Scheduler) DispatchComposeCleanup(ctx context.Context, project *store.Project, deploymentID uuid.UUID) (uuid.UUID, error) {
	if project.PinnedNodeID == nil {
		return uuid.Nil, nil // never deployed; nothing to clean up
	}
	task := &store.Task{
		Type:         store.TaskTypeCleanup,
		NodeID:       project.PinnedNodeID,
		DeploymentID: deploymentID,
		Payload: map[string]interface{}{
			"mode":              "compose",
			"project_id":        project.ID.String(),
			"domain_prefix":     project.DomainPrefix,
			"compose_file_path": project.ComposeFilePath,
		},
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

// TriggerDeployment is the canonical entry point for "create a Deployment row
// and dispatch the right build/deploy chain for the project". It is shared by
// the manual-deploy API handler, the external-repo poller, and the hosted-repo
// post-receive trigger so all three flows stay in lockstep.
//
// source is a free-form tag ("manual", "auto-poll", "auto-push") used only for
// logging; it does not affect dispatch behaviour.
func (s *Scheduler) TriggerDeployment(ctx context.Context, projectID uuid.UUID, source string) (*store.Deployment, error) {
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	if project.ProjectType == store.ProjectTypeDomainOnly {
		return nil, fmt.Errorf("domain_only projects cannot be deployed")
	}
	// Single gate for every deploy path (manual, auto-poll, auto-image,
	// git-push hook): a paused project must not be redeployed until resumed.
	if project.Paused {
		return nil, fmt.Errorf("project %s is paused", projectID)
	}
	deployment, err := s.store.CreateDeployment(ctx, &store.Deployment{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	// Compose and image projects skip the build phase: images are pulled by
	// `docker compose up` directly on the pinned deploy node. Image projects
	// reuse the same code path with a synthesised inline compose YAML.
	if project.ProjectType == store.ProjectTypeCompose || project.ProjectType == store.ProjectTypeImage {
		if err := s.DispatchDeploy(ctx, deployment, project, ""); err != nil {
			return nil, fmt.Errorf("dispatch deploy: %w", err)
		}
		return deployment, nil
	}
	if err := s.DispatchBuild(ctx, deployment, project); err != nil {
		return nil, fmt.Errorf("dispatch build: %w", err)
	}
	return deployment, nil
}

// DispatchBuild creates a build task on the best builder node.
func (s *Scheduler) DispatchBuild(ctx context.Context, deployment *store.Deployment, project *store.Project) error {
	if project.ProjectType == store.ProjectTypeDomainOnly {
		return fmt.Errorf("cannot dispatch build for domain_only project")
	}
	if project.ProjectType == store.ProjectTypeCompose {
		return fmt.Errorf("cannot dispatch build for compose project (compose deploys must use image: only)")
	}
	if project.ProjectType == store.ProjectTypeImage {
		return fmt.Errorf("cannot dispatch build for image project (pulls a pre-built image)")
	}
	builderNode, err := s.PickBuilderNode(ctx)
	if err != nil {
		return err
	}

	// Collect decrypted secrets; find git credentials (SSH key or HTTPS token).
	secrets, _ := s.store.GetProjectSecretsDecrypted(ctx, project.ID)
	var gitSSHKey, gitUsername, gitToken string
	buildSecrets := make(map[string]string)
	for _, sec := range secrets {
		if sec.UseForBuild && sec.BuildSecretID != "" {
			buildSecrets[sec.BuildSecretID] = sec.PlainValue
		}
		if sec.UseForGit {
			switch sec.SecretType {
			case store.SecretTypeSSHKey:
				gitSSHKey = sec.PlainValue
			case store.SecretTypePassword:
				gitUsername = sec.GitUsername
				gitToken = sec.PlainValue
			}
		}
	}

	// For hosted repos, use a relative path; the agent prepends its CONTROL_PLANE_URL.
	gitURL := project.GitURL
	gitBranch := project.GitBranch
	if project.GitSource == store.GitSourceHosted {
		gitURL = fmt.Sprintf("/git/%s.git", project.ID)
		gitUsername = "agent"
		gitToken = s.agentSecret
		gitSSHKey = ""
		if gitBranch == "" {
			gitBranch = "main"
		}
	}

	payload := map[string]interface{}{
		"git_url":         gitURL,
		"git_branch":      gitBranch,
		"dockerfile_path": project.DockerfilePath,
		"deployment_id":   deployment.ID.String(),
		"project_id":      project.ID.String(),
		"domain_prefix":   project.DomainPrefix,
	}
	if gitSSHKey != "" {
		payload["git_ssh_key"] = gitSSHKey
	}
	if gitUsername != "" && gitToken != "" {
		payload["git_username"] = gitUsername
		payload["git_token"] = gitToken
	}
	if len(buildSecrets) > 0 {
		payload["build_secrets"] = buildSecrets
	}

	task := &store.Task{
		Type:         store.TaskTypeBuild,
		NodeID:       &builderNode.ID,
		DeploymentID: deployment.ID,
		Payload:      payload,
	}
	_, err = s.store.CreateTask(ctx, task)
	return err
}

// DispatchDeploy selects a deploy node and creates a deploy task.
func (s *Scheduler) DispatchDeploy(ctx context.Context, deployment *store.Deployment, project *store.Project, imageTag string) error {
	if project.ProjectType == store.ProjectTypeDomainOnly {
		return fmt.Errorf("cannot dispatch deploy for domain_only project")
	}
	if project.ProjectType == store.ProjectTypeCompose {
		return s.dispatchComposeDeploy(ctx, deployment, project)
	}
	if project.ProjectType == store.ProjectTypeImage {
		return s.dispatchImageDeploy(ctx, deployment, project)
	}
	pds, err := s.store.GetProjectDatasets(ctx, project.ID)
	if err != nil {
		return err
	}
	var depDatasetIDs []uuid.UUID
	for _, pd := range pds {
		if pd.MountMode == store.MountModeDependency {
			depDatasetIDs = append(depDatasetIDs, pd.DatasetID)
		}
	}

	deployNode, err := s.resolveFixedDeployNode(ctx, project)
	if err != nil {
		return err
	}
	if deployNode == nil {
		deployNode, err = s.PickDeployNode(ctx, depDatasetIDs)
		if err != nil {
			return err
		}
	}
	if err := s.store.SetDeploymentNode(ctx, deployment.ID, deployNode.ID); err != nil {
		return err
	}

	// Build dataset mount list
	var datasets []map[string]interface{}
	for _, pd := range pds {
		ds, err := s.store.GetDataset(ctx, pd.DatasetID)
		if err != nil || ds == nil {
			continue
		}
		datasets = append(datasets, map[string]interface{}{
			"id":         ds.ID.String(),
			"name":       ds.Name,
			"nfs_path":   ds.NFSPath,
			"version":    ds.Version,
			"size_bytes": ds.SizeBytes,
			"mount_mode": string(pd.MountMode),
		})
	}

	// Collect env vars from secrets (all types with env_var_name set).
	secrets, _ := s.store.GetProjectSecretsDecrypted(ctx, project.ID)
	envVars := make(map[string]string)
	for _, sec := range secrets {
		if sec.EnvVarName != "" {
			envVars[sec.EnvVarName] = sec.PlainValue
		}
	}

	payload := map[string]interface{}{
		"image_tag":         imageTag,
		"deployment_id":     deployment.ID.String(),
		"project_id":        project.ID.String(),
		"domain_prefix":     project.DomainPrefix,
		"auth_required":     project.AuthRequired,
		"auth_domains":      project.AuthAllowedDomains,
		"container_port":    project.ContainerPort,
		"memory_limit":      project.MemoryLimit,
		"volume_mount_path": project.VolumeMountPath,
		"datasets":          datasets,
		"env_vars":          envVars,
	}
	if project.FixedHostPort != nil {
		payload["fixed_host_port"] = *project.FixedHostPort
	}
	task := &store.Task{
		Type:         store.TaskTypeDeploy,
		NodeID:       &deployNode.ID,
		DeploymentID: deployment.ID,
		Payload:      payload,
	}
	_, err = s.store.CreateTask(ctx, task)
	return err
}

// dispatchComposeDeploy schedules a docker-compose deploy on the project's
// pinned node. The first deploy picks any active deploy node and persists the
// pin on the project; subsequent deploys must run on the same node so docker
// named volumes survive across redeploys.
// buildRegistryAuthsPayload converts decrypted registry credentials into the
// JSON-friendly shape carried in a compose deploy task payload. Entries with an
// empty registry address are dropped (they cannot produce a usable docker auth).
func buildRegistryAuthsPayload(auths []store.RegistryAuth) []map[string]string {
	out := make([]map[string]string, 0, len(auths))
	for _, a := range auths {
		if a.Addr == "" {
			continue
		}
		out = append(out, map[string]string{
			"addr":     a.Addr,
			"username": a.Username,
			"password": a.Password,
		})
	}
	return out
}

func (s *Scheduler) dispatchComposeDeploy(ctx context.Context, deployment *store.Deployment, project *store.Project) error {
	if project.GitSource != store.GitSourceHosted && project.GitURL == "" {
		return fmt.Errorf("compose project has no git_url")
	}
	if project.ExposeService == "" || project.ExposePort == 0 {
		return fmt.Errorf("compose project must declare expose_service and expose_port")
	}

	deployNode, err := s.resolveFixedDeployNode(ctx, project)
	if err != nil {
		return err
	}
	if deployNode == nil {
		deployNode, err = s.pickOrReusePinnedNode(ctx, project)
		if err != nil {
			return err
		}
	}
	if err := s.store.SetDeploymentNode(ctx, deployment.ID, deployNode.ID); err != nil {
		return err
	}

	// Collect git credentials and project env vars (compose receives env vars
	// via a generated .env file that all services interpolate from).
	secrets, _ := s.store.GetProjectSecretsDecrypted(ctx, project.ID)
	var gitSSHKey, gitUsername, gitToken string
	envVars := make(map[string]string)
	for _, sec := range secrets {
		if sec.UseForGit {
			switch sec.SecretType {
			case store.SecretTypeSSHKey:
				gitSSHKey = sec.PlainValue
			case store.SecretTypePassword:
				gitUsername = sec.GitUsername
				gitToken = sec.PlainValue
			}
		}
		if sec.EnvVarName != "" {
			envVars[sec.EnvVarName] = sec.PlainValue
		}
	}

	// Collect the project owner's private-registry pull credentials so the agent
	// can pull private compose images (e.g. ghcr.io). These are tenant-level:
	// every project the owner has gets all of their registry credentials.
	ownerAuths, _ := s.store.GetUserRegistrySecretsDecrypted(ctx, project.OwnerID)
	registryAuths := buildRegistryAuthsPayload(ownerAuths)

	// For hosted repos, use a relative path; the agent prepends its CONTROL_PLANE_URL.
	gitURL := project.GitURL
	gitBranch := project.GitBranch
	if project.GitSource == store.GitSourceHosted {
		gitURL = fmt.Sprintf("/git/%s.git", project.ID)
		gitUsername = "agent"
		gitToken = s.agentSecret
		gitSSHKey = ""
		if gitBranch == "" {
			gitBranch = "main"
		}
	}

	payload := map[string]interface{}{
		"mode":              "compose",
		"deployment_id":     deployment.ID.String(),
		"project_id":        project.ID.String(),
		"domain_prefix":     project.DomainPrefix,
		"git_url":           gitURL,
		"git_branch":        gitBranch,
		"compose_file_path": project.ComposeFilePath,
		"expose_service":    project.ExposeService,
		"expose_port":       project.ExposePort,
		"memory_limit":      project.MemoryLimit,
		"env_vars":          envVars,
	}
	if project.FixedHostPort != nil {
		payload["fixed_host_port"] = *project.FixedHostPort
	}
	if gitSSHKey != "" {
		payload["git_ssh_key"] = gitSSHKey
	}
	if gitUsername != "" && gitToken != "" {
		payload["git_username"] = gitUsername
		payload["git_token"] = gitToken
	}
	if len(registryAuths) > 0 {
		payload["registry_auths"] = registryAuths
	}

	task := &store.Task{
		Type:         store.TaskTypeDeploy,
		NodeID:       &deployNode.ID,
		DeploymentID: deployment.ID,
		Payload:      payload,
	}
	_, err = s.store.CreateTask(ctx, task)
	return err
}

// dispatchImageDeploy schedules a deploy for an image-only project. It reuses
// the compose deployer on the agent side by synthesising a tiny inline
// docker-compose.yml from the project's image_ref + container_port — no git
// repo is involved. The agent receives `inline_compose_yaml` in its payload
// and skips the clone step.
func (s *Scheduler) dispatchImageDeploy(ctx context.Context, deployment *store.Deployment, project *store.Project) error {
	if project.ImageRef == "" {
		return fmt.Errorf("image project has no image_ref")
	}
	if project.ContainerPort <= 0 {
		return fmt.Errorf("image project has no container_port")
	}

	deployNode, err := s.resolveFixedDeployNode(ctx, project)
	if err != nil {
		return err
	}
	if deployNode == nil {
		deployNode, err = s.pickOrReusePinnedNode(ctx, project)
		if err != nil {
			return err
		}
	}
	if err := s.store.SetDeploymentNode(ctx, deployment.ID, deployNode.ID); err != nil {
		return err
	}

	secrets, _ := s.store.GetProjectSecretsDecrypted(ctx, project.ID)
	envVars := make(map[string]string)
	for _, sec := range secrets {
		if sec.EnvVarName != "" {
			envVars[sec.EnvVarName] = sec.PlainValue
		}
	}

	inlineCompose := buildInlineComposeYAML(project.ImageRef, project.ContainerPort)

	payload := map[string]interface{}{
		"mode":                "compose",
		"deployment_id":       deployment.ID.String(),
		"project_id":          project.ID.String(),
		"domain_prefix":       project.DomainPrefix,
		"inline_compose_yaml": inlineCompose,
		"expose_service":      "app",
		"expose_port":         project.ContainerPort,
		"volume_mount_path":   project.VolumeMountPath,
		"memory_limit":        project.MemoryLimit,
		"env_vars":            envVars,
	}
	if project.FixedHostPort != nil {
		payload["fixed_host_port"] = *project.FixedHostPort
	}

	task := &store.Task{
		Type:         store.TaskTypeDeploy,
		NodeID:       &deployNode.ID,
		DeploymentID: deployment.ID,
		Payload:      payload,
	}
	_, err = s.store.CreateTask(ctx, task)
	return err
}

// buildInlineComposeYAML returns a minimal docker-compose.yml for an image-only
// project: a single service named "app" bound to the given image. Workspace
// volume mounting (when volume_mount_path is set on the project) is injected
// agent-side via muvee.override.yml so the bind-mount points at the same NFS
// directory the Workspace API serves, matching the deployment-type pattern.
func buildInlineComposeYAML(imageRef string, containerPort int) string {
	var sb strings.Builder
	sb.WriteString("services:\n")
	sb.WriteString("  app:\n")
	sb.WriteString(fmt.Sprintf("    image: %s\n", imageRef))
	sb.WriteString("    restart: unless-stopped\n")
	// Inject bound project secrets: the agent always writes them to workDir/.env
	// (see deployer.DeployCompose) and runs compose with --project-directory workDir,
	// so env_file: .env resolves and delivers each key as a real container env var —
	// matching the deployment-type `docker run -e KEY=VALUE` path. Without this line
	// the synthesized service references nothing and image projects run secret-less.
	sb.WriteString("    env_file:\n")
	sb.WriteString("      - .env\n")
	_ = containerPort // ports are handled by the deployer's Traefik label injection
	return sb.String()
}

// resolveFixedDeployNode loads the deployer node a project is pinned to via
// admin-set fixed_node_id. Callers should fall back to the regular node picker
// when this returns (nil, nil). An error from this function means the fixed
// binding is broken (node missing/offline) and dispatch should abort.
func (s *Scheduler) resolveFixedDeployNode(ctx context.Context, project *store.Project) (*store.Node, error) {
	if project.FixedNodeID == nil {
		return nil, nil
	}
	node, err := s.store.GetNode(ctx, *project.FixedNodeID)
	if err != nil {
		return nil, fmt.Errorf("get fixed deploy node: %w", err)
	}
	if node == nil {
		return nil, fmt.Errorf("project's fixed deploy node %s no longer exists", project.FixedNodeID)
	}
	if time.Since(node.LastSeenAt) > 2*time.Minute {
		return nil, fmt.Errorf("project's fixed deploy node %q is offline; refusing to dispatch", node.Hostname)
	}
	return node, nil
}

// pickOrReusePinnedNode returns the project's pinned deploy node, picking and
// persisting one on first use. If the previously pinned node has gone offline,
// the dispatch fails rather than silently migrating (which would orphan the
// named volumes on the old node).
func (s *Scheduler) pickOrReusePinnedNode(ctx context.Context, project *store.Project) (*store.Node, error) {
	if project.PinnedNodeID != nil {
		node, err := s.store.GetNode(ctx, *project.PinnedNodeID)
		if err != nil {
			return nil, fmt.Errorf("get pinned node: %w", err)
		}
		if node == nil {
			return nil, fmt.Errorf("compose project's pinned deploy node no longer exists")
		}
		if time.Since(node.LastSeenAt) > 2*time.Minute {
			return nil, fmt.Errorf("compose project's pinned deploy node %q is offline; data is on that node, refusing to migrate", node.Hostname)
		}
		return node, nil
	}

	node, err := s.PickDeployNode(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetProjectPinnedNode(ctx, project.ID, node.ID); err != nil {
		return nil, fmt.Errorf("pin project to node: %w", err)
	}
	project.PinnedNodeID = &node.ID
	return node, nil
}
