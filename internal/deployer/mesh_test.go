package deployer

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

var testMesh = &MeshSpec{Network: "muvee-mesh", GatewayIP: "10.213.0.2", FallbackDNS: []string{"100.100.2.136", "100.100.2.138"}}

func TestMeshRunArgs(t *testing.T) {
	if got := meshRunArgs(nil); got != nil {
		t.Fatalf("nil mesh must add no flags, got %v", got)
	}
	want := []string{"--network", "muvee-mesh", "--dns", "10.213.0.2", "--dns", "100.100.2.136", "--dns", "100.100.2.138"}
	if got := meshRunArgs(testMesh); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBuildComposeOverrideYAMLWithMesh(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, "/v:/w:rw", "1g", testMesh)

	var parsed struct {
		Services map[string]struct {
			Ports    []string          `yaml:"ports"`
			Labels   map[string]string `yaml:"labels"`
			Volumes  []string          `yaml:"volumes"`
			MemLimit string            `yaml:"mem_limit"`
			Networks []string          `yaml:"networks"`
			DNS      []string          `yaml:"dns"`
		} `yaml:"services"`
		Networks map[string]struct {
			External bool `yaml:"external"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("override is not valid YAML: %v\n%s", err, got)
	}
	app := parsed.Services["app"]
	if !reflect.DeepEqual(app.Networks, []string{"default", "muvee-mesh"}) {
		t.Errorf("networks = %v (default must be kept)", app.Networks)
	}
	if !reflect.DeepEqual(app.DNS, []string{"10.213.0.2", "100.100.2.136", "100.100.2.138"}) {
		t.Errorf("dns = %v", app.DNS)
	}
	if !parsed.Networks["muvee-mesh"].External {
		t.Errorf("muvee-mesh must be declared external at top level:\n%s", got)
	}
	// Existing keys are unaffected by the mesh block.
	if len(app.Ports) != 1 || app.Labels["muvee.domain_prefix"] != "foxy" || len(app.Volumes) != 1 || app.MemLimit != "1g" {
		t.Errorf("existing override keys broken:\n%s", got)
	}
}

func TestBuildComposeOverrideYAMLWithoutMeshHasNoNetworks(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, "", "", nil)
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["networks"]; ok {
		t.Fatalf("no mesh → no networks block:\n%s", got)
	}
}
