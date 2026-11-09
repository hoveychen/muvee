package mesh

import (
	"bufio"
	"context"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// callerLookup maps a source IP on the mesh network to the domain_prefix of
// the project container that owns it ("" when unknown).
type callerLookup interface {
	Lookup(ctx context.Context, ip string) string
}

// dockerCallers keeps an IP → domain_prefix table built from the containers
// attached to muvee-mesh that carry the muvee.domain_prefix label. The table
// is refreshed every ttl, and on a miss at most once per minMissInterval so a
// freshly started caller is recognised without hammering docker. watch()
// additionally drops the table on every connect/disconnect on the network:
// docker reuses a stopped container's IP, and a stale entry would let the
// next container on that IP borrow the old project's identity.
type dockerCallers struct {
	ttl             time.Duration
	minMissInterval time.Duration
	list            func(ctx context.Context) (map[string]string, error)

	mu        sync.Mutex
	table     map[string]string
	refreshed time.Time
}

func newDockerCallers() *dockerCallers {
	return &dockerCallers{ttl: 10 * time.Second, minMissInterval: time.Second, list: listMeshCallers}
}

func (d *dockerCallers) Lookup(ctx context.Context, ip string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	age := time.Since(d.refreshed)
	prefix, ok := d.table[ip]
	if age < d.ttl && (ok || age < d.minMissInterval) {
		return prefix
	}
	if table, err := d.list(ctx); err == nil {
		d.table, d.refreshed = table, time.Now()
	} else {
		// Never answer from a table we know may be stale.
		d.table = nil
		log.Printf("mesh: list mesh containers: %v", err)
	}
	return d.table[ip]
}

func (d *dockerCallers) invalidate() {
	d.mu.Lock()
	d.table, d.refreshed = nil, time.Time{}
	d.mu.Unlock()
}

// watch invalidates the table on every network event of the mesh network
// until ctx is done, restarting `docker events` if it exits.
func (d *dockerCallers) watch(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "docker", "events", "--format", "{{.Action}}",
			"--filter", "type=network", "--filter", "network="+NetworkName)
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			// Anything may have changed while we were not subscribed.
			d.invalidate()
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				d.invalidate()
			}
			err = cmd.Wait()
		}
		if ctx.Err() == nil {
			log.Printf("mesh: docker events stopped (%v); restarting", err)
			d.invalidate()
			time.Sleep(2 * time.Second)
		}
	}
}

// listMeshCallers queries docker for every labelled container on the mesh.
func listMeshCallers(ctx context.Context) (map[string]string, error) {
	ids, err := dockerCmd(ctx, "ps", "-q", "--no-trunc", "--filter", "network="+NetworkName, "--filter", "label=muvee.domain_prefix")
	if err != nil {
		return nil, err
	}
	if ids == "" {
		return map[string]string{}, nil
	}
	args := append([]string{"inspect", "-f",
		`{{index .Config.Labels "muvee.domain_prefix"}} {{with index .NetworkSettings.Networks "` + NetworkName + `"}}{{.IPAddress}}{{end}}`},
		strings.Fields(ids)...)
	out, err := dockerCmd(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseCallerTable(out), nil
}

// parseCallerTable parses "<prefix> <ip>" lines from docker inspect.
func parseCallerTable(out string) map[string]string {
	table := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			table[f[1]] = f[0]
		}
	}
	return table
}
