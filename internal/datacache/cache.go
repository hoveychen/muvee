package datacache

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// Cache manages local dataset storage on deploy nodes.
type Cache struct {
	store   *store.Store
	nodeID  uuid.UUID
	baseDir string // e.g. /muvee/data
}

func New(st *store.Store, nodeID uuid.UUID, baseDir string) *Cache {
	return &Cache{store: st, nodeID: nodeID, baseDir: baseDir}
}

// objectDir returns the local path for a dataset version's data.
func (c *Cache) objectDir(datasetID uuid.UUID, version int64) string {
	return filepath.Join(c.baseDir, "objects", datasetID.String(), fmt.Sprintf("v%d", version))
}

// mountDir returns the symlink directory for a specific deployment.
func (c *Cache) mountDir(deploymentID uuid.UUID) string {
	return filepath.Join(c.baseDir, "mounts", deploymentID.String())
}

// EnsureDataset rsync's the dataset from NFS if not already cached at the right version,
// then returns the local object directory path.
func (c *Cache) EnsureDataset(ctx context.Context, ds *store.Dataset) (string, error) {
	target := c.objectDir(ds.ID, ds.Version)
	if _, err := os.Stat(target); err == nil {
		// Already cached – update last_used_at
		_ = c.store.TouchNodeDataset(ctx, c.nodeID, ds.ID, ds.SizeBytes)
		return target, nil
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", target, err)
	}

	fmt.Printf("datacache: rsync %s -> %s\n", ds.NFSPath, target)
	cmd := exec.CommandContext(ctx, "rsync", "-a", "--delete",
		ds.NFSPath+"/", target+"/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("rsync dataset %s: %w", ds.Name, err)
	}

	_ = c.store.TouchNodeDataset(ctx, c.nodeID, ds.ID, ds.SizeBytes)
	return target, nil
}

// SetupMounts creates symlinks in the deployment mount directory for all dependency datasets,
// and returns a list of docker volume mount strings for readwrite datasets.
// Returns: (dependency mounts []string, readwrite mounts []string, error)
func (c *Cache) SetupMounts(ctx context.Context, deploymentID uuid.UUID, datasets []DatasetMount) ([]string, []string, error) {
	mountBase := c.mountDir(deploymentID)
	if err := os.MkdirAll(mountBase, 0755); err != nil {
		return nil, nil, err
	}

	var depMounts []string
	var rwMounts []string

	for _, dm := range datasets {
		containerPath := "/data/" + dm.Dataset.Name
		if dm.MountMode == store.MountModeDependency {
			localPath, err := c.EnsureDataset(ctx, dm.Dataset)
			if err != nil {
				return nil, nil, err
			}
			linkPath := filepath.Join(mountBase, dm.Dataset.Name)
			_ = os.Remove(linkPath)
			if err := os.Symlink(localPath, linkPath); err != nil {
				return nil, nil, fmt.Errorf("symlink %s: %w", linkPath, err)
			}
			depMounts = append(depMounts, fmt.Sprintf("%s:%s:ro", linkPath, containerPath))
		} else {
			// readwrite: direct NFS mount
			rwMounts = append(rwMounts, fmt.Sprintf("%s:%s:rw", dm.Dataset.NFSPath, containerPath))
		}
	}
	return depMounts, rwMounts, nil
}

// CleanupMounts removes the deployment's mount symlink directory.
func (c *Cache) CleanupMounts(deploymentID uuid.UUID) error {
	return os.RemoveAll(c.mountDir(deploymentID))
}

// EvictLRU removes the least recently used dependency datasets from this node until
// at least bytesNeeded bytes are freed.
func (c *Cache) EvictLRU(ctx context.Context, bytesNeeded int64) error {
	evictList, err := c.store.GetLRUDatasetsForNode(ctx, c.nodeID, bytesNeeded)
	if err != nil {
		return err
	}
	for _, nd := range evictList {
		// Remove all versioned copies for this dataset
		dsDir := filepath.Join(c.baseDir, "objects", nd.DatasetID.String())
		if err := os.RemoveAll(dsDir); err != nil {
			return fmt.Errorf("evict dataset %s: %w", nd.DatasetID, err)
		}
		if err := c.store.RemoveNodeDataset(ctx, c.nodeID, nd.DatasetID); err != nil {
			return err
		}
		fmt.Printf("datacache: evicted dataset %s (%d bytes)\n", nd.DatasetID, nd.SizeBytes)
	}
	return nil
}

type DatasetMount struct {
	Dataset   *store.Dataset
	MountMode store.MountMode
}
