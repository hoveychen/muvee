package mesh

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// foreignHolder records that another muvee stack's container held our gateway
// IP on a mesh network.
type foreignHolder struct {
	Network string    `json:"network"` // network name, for humans and pruning
	Holder  string    `json:"holder"`  // container name seen holding the IP
	Stack   string    `json:"stack"`   // its stack identity (see stackIdentity)
	SeenAt  time.Time `json:"seen_at"`
}

// holderFileName lives in the agent's DATA_DIR, which survives agent
// container recreation (a named volume in the stock compose files).
const holderFileName = "mesh-foreign-gateway-holders.json"

// holderMemory remembers, per docker network ID, that another stack holds our
// gateway IP there. Once remembered we never `connect --ip` on that network
// again, even when the IP looks free: Watchtower recreates the other stack's
// agent by removing the old container before creating the new one, and
// grabbing the IP in that gap leaves the other (healthy) stack's new agent
// stuck in Created with "Address already in use".
//
// Keyed by network ID, so recreating the network (which is what moving one
// stack to its own MESH_NETWORK/MESH_SUBNET amounts to, or a manual
// `docker network rm`) drops the record by itself. An empty path keeps the
// memory in-process only.
type holderMemory struct {
	path string

	mu     sync.Mutex
	loaded bool
	m      map[string]foreignHolder // network ID -> holder
}

func newHolderMemory(dir string) *holderMemory {
	h := &holderMemory{m: map[string]foreignHolder{}}
	if dir != "" {
		h.path = filepath.Join(dir, holderFileName)
	}
	return h
}

func (h *holderMemory) loadLocked() {
	if h.loaded || h.path == "" {
		h.loaded = true
		return
	}
	h.loaded = true
	b, err := os.ReadFile(h.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("mesh: WARNING: cannot read %s: %v (foreign gateway holders seen before this restart are forgotten)", h.path, err)
		}
		return
	}
	if err := json.Unmarshal(b, &h.m); err != nil || h.m == nil {
		log.Printf("mesh: WARNING: cannot parse %s: %v (foreign gateway holders seen before this restart are forgotten)", h.path, err)
		h.m = map[string]foreignHolder{}
	}
}

func (h *holderMemory) saveLocked() error {
	if h.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(h.m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}

// lookup returns the remembered foreign holder for networkID. Records for an
// earlier network of the same name (a different ID: it was recreated) are
// dropped on the way.
func (h *holderMemory) lookup(network, networkID string) (foreignHolder, bool) {
	if h == nil {
		return foreignHolder{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	pruned := false
	for id, r := range h.m {
		if r.Network == network && id != networkID {
			delete(h.m, id)
			pruned = true
		}
	}
	if pruned {
		if err := h.saveLocked(); err != nil {
			log.Printf("mesh: WARNING: cannot update %s: %v", h.path, err)
		}
	}
	r, ok := h.m[networkID]
	return r, ok
}

// remember records a foreign holder; the record is kept in memory even when
// the file cannot be written (the error says so).
func (h *holderMemory) remember(networkID string, r foreignHolder) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	h.m[networkID] = r
	if err := h.saveLocked(); err != nil {
		return fmt.Errorf("persist to %s: %w (remembered until the agent restarts only)", h.path, err)
	}
	return nil
}

// resetHint tells an operator how to make the agent try again.
func (h *holderMemory) resetHint() string {
	if h == nil || h.path == "" {
		return "restart the agent or change MESH_NETWORK"
	}
	return "delete " + h.path + " in the agent container (DATA_DIR) and restart the agent, or change MESH_NETWORK"
}
