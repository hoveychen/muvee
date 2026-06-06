package deployer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RegistryAuth is a private container-registry pull credential supplied by a
// project owner. The agent uses it to build a per-deploy docker config so
// compose can pull private images.
type RegistryAuth struct {
	Addr     string
	Username string
	Password string
}

// buildDockerConfigJSON merges the given registry auths on top of an existing
// docker config (baseJSON, which may be empty/nil) and returns the resulting
// config.json bytes. Each auth becomes an `auths[addr]` entry whose `auth` field
// is base64(username:password). Existing entries for the same address are
// overwritten; other base config (credHelpers, unrelated auths) is preserved.
func buildDockerConfigJSON(baseJSON []byte, auths []RegistryAuth) ([]byte, error) {
	cfg := map[string]interface{}{}
	if len(baseJSON) > 0 {
		if err := json.Unmarshal(baseJSON, &cfg); err != nil {
			return nil, fmt.Errorf("parse base docker config: %w", err)
		}
	}
	authsMap, ok := cfg["auths"].(map[string]interface{})
	if !ok {
		authsMap = map[string]interface{}{}
	}
	for _, a := range auths {
		if a.Addr == "" {
			continue
		}
		token := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
		authsMap[a.Addr] = map[string]interface{}{"auth": token}
	}
	cfg["auths"] = authsMap
	return json.MarshalIndent(cfg, "", "  ")
}

// readDockerConfigJSON returns the bytes of the docker config.json the agent
// itself uses (honouring DOCKER_CONFIG, falling back to $HOME/.docker), or nil
// if none exists. Errors other than "not found" are returned.
func readDockerConfigJSON() ([]byte, error) {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil
		}
		dir = filepath.Join(home, ".docker")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// prepareRegistryDockerConfig writes a temporary docker config directory seeded
// from the agent's own config and augmented with the given registry auths. It
// returns the directory path (to be set as DOCKER_CONFIG) and a cleanup func.
// When auths is empty it returns an empty path and a no-op cleanup so callers
// fall back to the agent's default docker config.
func prepareRegistryDockerConfig(auths []RegistryAuth) (string, func(), error) {
	if len(auths) == 0 {
		return "", func() {}, nil
	}
	base, err := readDockerConfigJSON()
	if err != nil {
		return "", func() {}, fmt.Errorf("read agent docker config: %w", err)
	}
	merged, err := buildDockerConfigJSON(base, auths)
	if err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "muvee-dockercfg-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp docker config dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, "config.json"), merged, 0600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write temp docker config: %w", err)
	}
	return dir, cleanup, nil
}
