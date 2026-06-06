package main

import (
	"encoding/json"
	"testing"
)

// TestParseRegistryAuths exercises the payload shape exactly as it arrives at
// the agent: registry_auths is a JSON array of objects, decoded into
// []interface{} of map[string]interface{}.
func TestParseRegistryAuths(t *testing.T) {
	raw := `{"registry_auths":[
		{"addr":"ghcr.io","username":"alice","password":"pw1"},
		{"addr":"","username":"skip","password":"me"},
		{"addr":"quay.io","username":"bob","password":"pw2"}
	]}`
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	got := parseRegistryAuths(p)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (empty-addr entry dropped)", len(got))
	}
	if got[0].Addr != "ghcr.io" || got[0].Username != "alice" || got[0].Password != "pw1" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Addr != "quay.io" || got[1].Password != "pw2" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestParseRegistryAuths_Missing(t *testing.T) {
	if got := parseRegistryAuths(map[string]interface{}{}); got != nil {
		t.Errorf("got %v, want nil when registry_auths absent", got)
	}
}
