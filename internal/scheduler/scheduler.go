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
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{store: st}
}

// SetGitHostingConfig configures the scheduler for hosted git repo builds.
func (s *Scheduler) SetGitHostingConfig(agentSecret string) {
	s.agentSecret = agentSecret
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

// DispatchBuild creates a build task on the best builder node.
func (s *Scheduler) DispatchBuild(ctx context.Context, deployment *store.Deployment, project *store.Project) error {
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
