package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// muveeContainerStatus is the slice of `docker inspect` output that the agent
// ships to the control plane on every status tick.
type muveeContainerStatus struct {
	DomainPrefix string
	RestartCount int
	OOMKilled    bool
	// HostPort is the host-side port that maps to the container's expose port.
	// 0 when the container is stopped, the expose_port label is missing/invalid,
	// or no host binding exists yet.
	HostPort int
}

// dockerPortBinding mirrors one entry of `NetworkSettings.Ports[port/proto]`.
type dockerPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// dockerInspectPayload is the minimal subset of `docker inspect --format '{{json .}}'`
// output that the agent needs.
type dockerInspectPayload struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		RestartCount int  `json:"RestartCount"`
		OOMKilled    bool `json:"OOMKilled"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]dockerPortBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

// parseMuveeContainerInspect pulls the muvee status fields out of a single
// container's `docker inspect --format '{{json .}}'` output. ok is false (no
// error) when the container lacks the muvee.domain_prefix label, so the caller
// can simply skip non-muvee containers without erroring out.
func parseMuveeContainerInspect(raw []byte) (status muveeContainerStatus, ok bool, err error) {
	var v dockerInspectPayload
	if err := json.Unmarshal(raw, &v); err != nil {
		return muveeContainerStatus{}, false, err
	}
	prefix := v.Config.Labels["muvee.domain_prefix"]
	if prefix == "" {
		return muveeContainerStatus{}, false, nil
	}
	status = muveeContainerStatus{
		DomainPrefix: prefix,
		RestartCount: v.State.RestartCount,
		OOMKilled:    v.State.OOMKilled,
	}
	if portStr := v.Config.Labels["muvee.expose_port"]; portStr != "" {
		if cp, parseErr := strconv.Atoi(portStr); parseErr == nil && cp > 0 {
			status.HostPort = pickHostPort(v.NetworkSettings.Ports, cp)
		}
	}
	return status, true, nil
}

// pickHostPort returns the host port published for the given container port,
// preferring an IPv4 (0.0.0.0) binding over IPv6. Returns 0 when no usable
// binding exists.
func pickHostPort(ports map[string][]dockerPortBinding, containerPort int) int {
	bindings, ok := ports[fmt.Sprintf("%d/tcp", containerPort)]
	if !ok || len(bindings) == 0 {
		return 0
	}
	var fallback int
	for _, b := range bindings {
		if b.HostPort == "" {
			continue
		}
		p, err := strconv.Atoi(b.HostPort)
		if err != nil || p <= 0 {
			continue
		}
		if b.HostIP == "0.0.0.0" || b.HostIP == "" {
			return p
		}
		if fallback == 0 {
			fallback = p
		}
	}
	return fallback
}
