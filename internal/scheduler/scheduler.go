package scheduler

import (
	"context"
	"fmt"
	"sort"
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

// DispatchComposeCleanup tears down a compose project on its pinned node,
// including its docker named volumes. Used when a compose project is deleted.
// deploymentID is optional — if zero, the cleanup is not tied to a specific
// deployment row.
func (s *Scheduler) DispatchComposeCleanup(ctx context.Context, project *store.Project, deploymentID uuid.UUID) error {
	if project.PinnedNodeID == nil {
		return nil // never deployed; nothing to clean up
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
	_, err := s.store.CreateTask(ctx, task)
	return err
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
	deployment, err := s.store.CreateDeployment(ctx, &store.Deployment{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	// Compose projects skip the build phase: images are pulled by `docker
	// compose up` directly on the pinned deploy node.
	if project.ProjectType == store.ProjectTypeCompose {
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

	deployNode, err := s.PickDeployNode(ctx, depDatasetIDs)
	if err != nil {
		return err
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

	task := &store.Task{
		Type:         store.TaskTypeDeploy,
		NodeID:       &deployNode.ID,
		DeploymentID: deployment.ID,
		Payload: map[string]interface{}{
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
		},
	}
	_, err = s.store.CreateTask(ctx, task)
	return err
}

// dispatchComposeDeploy schedules a docker-compose deploy on the project's
// pinned node. The first deploy picks any active deploy node and persists the
// pin on the project; subsequent deploys must run on the same node so docker
// named volumes survive across redeploys.
func (s *Scheduler) dispatchComposeDeploy(ctx context.Context, deployment *store.Deployment, project *store.Project) error {
	if project.GitURL == "" {
		return fmt.Errorf("compose project has no git_url")
	}
	if project.ExposeService == "" || project.ExposePort == 0 {
		return fmt.Errorf("compose project must declare expose_service and expose_port")
	}

	deployNode, err := s.pickOrReusePinnedNode(ctx, project)
	if err != nil {
		return err
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

	payload := map[string]interface{}{
		"mode":              "compose",
		"deployment_id":     deployment.ID.String(),
		"project_id":        project.ID.String(),
		"domain_prefix":     project.DomainPrefix,
		"git_url":           project.GitURL,
		"git_branch":        project.GitBranch,
		"compose_file_path": project.ComposeFilePath,
		"expose_service":    project.ExposeService,
		"expose_port":       project.ExposePort,
		"env_vars":          envVars,
	}
	if gitSSHKey != "" {
		payload["git_ssh_key"] = gitSSHKey
	}
	if gitUsername != "" && gitToken != "" {
		payload["git_username"] = gitUsername
		payload["git_token"] = gitToken
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
