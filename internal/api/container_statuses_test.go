package api

import (
	"encoding/json"
	"testing"
)

func TestContainerStatusReport_DecodesHostPort(t *testing.T) {
	body := []byte(`[{"domain_prefix":"x","restart_count":3,"oom_killed":true,"host_port":32768}]`)
	var got []containerStatusReport
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].HostPort != 32768 {
		t.Errorf("HostPort=%d, want 32768 (json tag may be missing)", got[0].HostPort)
	}
	if got[0].RestartCount != 3 || !got[0].OOMKilled || got[0].DomainPrefix != "x" {
		t.Errorf("other fields decoded wrong: %+v", got[0])
	}
}

func TestContainerStatusReport_LegacyPayloadHostPortDefaults(t *testing.T) {
	body := []byte(`[{"domain_prefix":"x","restart_count":1,"oom_killed":false}]`)
	var got []containerStatusReport
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got[0].HostPort != 0 {
		t.Errorf("legacy payload should default HostPort=0, got %d", got[0].HostPort)
	}
}

func TestShouldRefreshHostPort(t *testing.T) {
	cases := []struct {
		name     string
		hostPort int
		want     bool
	}{
		{"unreported (0)", 0, false},
		{"negative", -1, false},
		{"valid ephemeral", 32768, true},
		{"max valid", 65535, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldRefreshHostPort(containerStatusReport{HostPort: c.hostPort})
			if got != c.want {
				t.Errorf("shouldRefreshHostPort(HostPort=%d) = %v, want %v", c.hostPort, got, c.want)
			}
		})
	}
}
