package main

import "testing"

const inspectFixtureRunning = `{
	"Config": {
		"Labels": {
			"muvee.domain_prefix": "myapp",
			"muvee.expose_port": "8080"
		}
	},
	"State": {
		"RestartCount": 3,
		"OOMKilled": false
	},
	"NetworkSettings": {
		"Ports": {
			"8080/tcp": [
				{"HostIp": "0.0.0.0", "HostPort": "32768"},
				{"HostIp": "::", "HostPort": "32768"}
			]
		}
	}
}`

const inspectFixtureStoppedNoBindings = `{
	"Config": {
		"Labels": {
			"muvee.domain_prefix": "myapp",
			"muvee.expose_port": "8080"
		}
	},
	"State": {"RestartCount": 5, "OOMKilled": true},
	"NetworkSettings": {"Ports": {}}
}`

const inspectFixtureNotMuvee = `{
	"Config": {"Labels": {"some.other.label": "x"}},
	"State": {"RestartCount": 0, "OOMKilled": false},
	"NetworkSettings": {"Ports": {}}
}`

const inspectFixtureMissingExposePortLabel = `{
	"Config": {"Labels": {"muvee.domain_prefix": "myapp"}},
	"State": {"RestartCount": 1, "OOMKilled": false},
	"NetworkSettings": {
		"Ports": {"8080/tcp": [{"HostIp": "0.0.0.0", "HostPort": "32768"}]}
	}
}`

const inspectFixtureMultiPort = `{
	"Config": {
		"Labels": {
			"muvee.domain_prefix": "myapp",
			"muvee.expose_port": "9090"
		}
	},
	"State": {"RestartCount": 0, "OOMKilled": false},
	"NetworkSettings": {
		"Ports": {
			"8080/tcp": [{"HostIp": "0.0.0.0", "HostPort": "32768"}],
			"9090/tcp": [{"HostIp": "0.0.0.0", "HostPort": "32769"}]
		}
	}
}`

const inspectFixtureIPv6Only = `{
	"Config": {
		"Labels": {
			"muvee.domain_prefix": "myapp",
			"muvee.expose_port": "8080"
		}
	},
	"State": {"RestartCount": 0, "OOMKilled": false},
	"NetworkSettings": {
		"Ports": {"8080/tcp": [{"HostIp": "::", "HostPort": "40000"}]}
	}
}`

const inspectFixtureBadExposePortLabel = `{
	"Config": {
		"Labels": {
			"muvee.domain_prefix": "myapp",
			"muvee.expose_port": "abc"
		}
	},
	"State": {"RestartCount": 0, "OOMKilled": false},
	"NetworkSettings": {
		"Ports": {"8080/tcp": [{"HostIp": "0.0.0.0", "HostPort": "32768"}]}
	}
}`

func TestParseMuveeContainerInspect_Running(t *testing.T) {
	st, ok, err := parseMuveeContainerInspect([]byte(inspectFixtureRunning))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for muvee container")
	}
	if st.DomainPrefix != "myapp" {
		t.Errorf("DomainPrefix=%q, want %q", st.DomainPrefix, "myapp")
	}
	if st.RestartCount != 3 {
		t.Errorf("RestartCount=%d, want 3", st.RestartCount)
	}
	if st.OOMKilled {
		t.Errorf("OOMKilled=true, want false")
	}
	if st.HostPort != 32768 {
		t.Errorf("HostPort=%d, want 32768", st.HostPort)
	}
}

func TestParseMuveeContainerInspect_StoppedKeepsRestartFields(t *testing.T) {
	st, ok, err := parseMuveeContainerInspect([]byte(inspectFixtureStoppedNoBindings))
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if st.HostPort != 0 {
		t.Errorf("HostPort=%d, want 0 (no bindings)", st.HostPort)
	}
	if st.RestartCount != 5 || !st.OOMKilled {
		t.Errorf("runtime fields not preserved: %+v", st)
	}
}

func TestParseMuveeContainerInspect_NotMuveeReturnsOkFalse(t *testing.T) {
	_, ok, err := parseMuveeContainerInspect([]byte(inspectFixtureNotMuvee))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("expected ok=false for non-muvee container")
	}
}

func TestParseMuveeContainerInspect_MissingExposePortLabel(t *testing.T) {
	st, ok, err := parseMuveeContainerInspect([]byte(inspectFixtureMissingExposePortLabel))
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if st.HostPort != 0 {
		t.Errorf("HostPort=%d, want 0 (no expose_port label)", st.HostPort)
	}
}

func TestParseMuveeContainerInspect_MultiPort(t *testing.T) {
	st, _, err := parseMuveeContainerInspect([]byte(inspectFixtureMultiPort))
	if err != nil {
		t.Fatal(err)
	}
	if st.HostPort != 32769 {
		t.Errorf("HostPort=%d, want 32769 (the 9090/tcp mapping)", st.HostPort)
	}
}

func TestParseMuveeContainerInspect_IPv6Fallback(t *testing.T) {
	st, _, err := parseMuveeContainerInspect([]byte(inspectFixtureIPv6Only))
	if err != nil {
		t.Fatal(err)
	}
	if st.HostPort != 40000 {
		t.Errorf("HostPort=%d, want 40000 (IPv6 fallback)", st.HostPort)
	}
}

func TestParseMuveeContainerInspect_BadExposePortLabel(t *testing.T) {
	st, _, err := parseMuveeContainerInspect([]byte(inspectFixtureBadExposePortLabel))
	if err != nil {
		t.Fatal(err)
	}
	if st.HostPort != 0 {
		t.Errorf("HostPort=%d, want 0 (unparseable expose_port label)", st.HostPort)
	}
}

func TestParseMuveeContainerInspect_InvalidJSON(t *testing.T) {
	_, _, err := parseMuveeContainerInspect([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
