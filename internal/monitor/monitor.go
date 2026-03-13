package monitor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

type FileEntry struct {
	Path     string
	Size     int64
	Checksum string
	Mtime    time.Time
}

type Monitor struct {
	store    *store.Store
	interval time.Duration
	workers  int
}

func New(st *store.Store, interval time.Duration, workers int) *Monitor {
	if workers <= 0 {
		workers = 4
	}
	return &Monitor{store: st, interval: interval, workers: workers}
}

func (m *Monitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.ScanAll(ctx)
		}
	}
}

func (m *Monitor) ScanAll(ctx context.Context) {
	datasets, err := m.store.ListDatasetsForUser(ctx, uuid.Nil, true)
	if err != nil {
		return
	}
	for _, ds := range datasets {
		if err := m.ScanDataset(ctx, ds); err != nil {
			fmt.Printf("monitor: scan dataset %s (%s) error: %v\n", ds.Name, ds.ID, err)
		}
	}
}

func (m *Monitor) ScanDataset(ctx context.Context, ds *store.Dataset) error {
	// Walk NFS path concurrently
	current, err := m.walkDir(ds.NFSPath)
	if err != nil {
		return fmt.Errorf("walk %s: %w", ds.NFSPath, err)
	}

	// Load previous snapshot files from history
	prevSnap, err := m.store.GetLatestSnapshot(ctx, ds.ID)
	if err != nil {
		return err
	}

	var events []*store.DatasetFileHistory
	var totalSize int64
	for _, f := range current {
		totalSize += f.Size
	}

	// Build previous file map from history
	prevFiles := make(map[string]*store.DatasetFileHistory)
	if prevSnap != nil {
		history, err := m.store.ListFileHistory(ctx, ds.ID, "", 100000)
		if err != nil {
			return err
		}
		// Replay to get current state per file
		for _, h := range history {
			if h.EventType == store.FileEventDeleted {
				delete(prevFiles, h.FilePath)
			} else {
				prevFiles[h.FilePath] = h
			}
		}
	}

	// Create snapshot record
	snap := &store.DatasetSnapshot{
		DatasetID:      ds.ID,
		TotalFiles:     int64(len(current)),
		TotalSizeBytes: totalSize,
		Version:        ds.Version,
	}
	snap, err = m.store.CreateDatasetSnapshot(ctx, snap)
	if err != nil {
		return err
	}

	// Diff: added and modified
	currentMap := make(map[string]*FileEntry, len(current))
	for i := range current {
		fe := &current[i]
		currentMap[fe.Path] = fe
		prev, exists := prevFiles[fe.Path]
		if !exists {
			events = append(events, &store.DatasetFileHistory{
				DatasetID:   ds.ID,
				FilePath:    fe.Path,
				EventType:   store.FileEventAdded,
				NewSize:     fe.Size,
				NewChecksum: fe.Checksum,
				SnapshotID:  snap.ID,
			})
		} else if prev.NewChecksum != fe.Checksum {
			events = append(events, &store.DatasetFileHistory{
				DatasetID:   ds.ID,
				FilePath:    fe.Path,
				EventType:   store.FileEventModified,
				OldSize:     prev.NewSize,
				NewSize:     fe.Size,
				OldChecksum: prev.NewChecksum,
				NewChecksum: fe.Checksum,
				SnapshotID:  snap.ID,
			})
		}
	}

	// Diff: deleted
	for path, prev := range prevFiles {
		if _, exists := currentMap[path]; !exists {
			events = append(events, &store.DatasetFileHistory{
				DatasetID:   ds.ID,
				FilePath:    path,
				EventType:   store.FileEventDeleted,
				OldSize:     prev.NewSize,
				OldChecksum: prev.NewChecksum,
				SnapshotID:  snap.ID,
			})
		}
	}

	if len(events) > 0 {
		if err := m.store.BulkInsertFileHistory(ctx, events); err != nil {
			return err
		}
		newVersion, err := m.store.IncrementDatasetVersion(ctx, ds.ID)
		if err != nil {
			return err
		}
		snap.Version = newVersion
		fmt.Printf("monitor: dataset %s: %d changes, new version %d\n", ds.Name, len(events), newVersion)
	}

	// Update dataset size
	ds.SizeBytes = totalSize
	return m.store.UpdateDataset(ctx, ds)
}

func (m *Monitor) walkDir(root string) ([]FileEntry, error) {
	type result struct {
		entry FileEntry
		err   error
	}

	type job struct {
		path string
		info os.FileInfo
	}

	var entries []FileEntry
	var mu sync.Mutex
	jobs := make(chan job, 1000)
	results := make(chan result, 1000)

	// Collect all file paths
	var wg sync.WaitGroup
	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				entry, err := m.processFile(j.path, j.info)
				results <- result{entry: entry, err: err}
			}
		}()
	}

	go func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			jobs <- job{path: rel, info: info}
			return nil
		})
		close(jobs)
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			continue
		}
		mu.Lock()
		entries = append(entries, r.entry)
		mu.Unlock()
	}
	return entries, nil
}

func (m *Monitor) processFile(relPath string, info os.FileInfo) (FileEntry, error) {
	entry := FileEntry{
		Path:  relPath,
		Size:  info.Size(),
		Mtime: info.ModTime(),
	}
	// Always compute checksum for correctness
	checksum, err := fileChecksum(relPath)
	if err != nil {
		return entry, err
	}
	entry.Checksum = checksum
	return entry, nil
}

func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}
