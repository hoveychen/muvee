package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRenderVersion_Plain_ServerOK(t *testing.T) {
	var buf bytes.Buffer
	renderVersion(&buf, "v1.2.3", "v9.8.7", nil, false)
	got := buf.String()
	if !strings.Contains(got, "Client Version: v1.2.3") {
		t.Fatalf("missing Client Version line, got:\n%s", got)
	}
	if !strings.Contains(got, "Server Version: v9.8.7") {
		t.Fatalf("missing Server Version line, got:\n%s", got)
	}
}

func TestRenderVersion_Plain_ServerFailureShowsUnavailable(t *testing.T) {
	var buf bytes.Buffer
	renderVersion(&buf, "v1.2.3", "", errors.New("not logged in"), false)
	got := buf.String()
	if !strings.Contains(got, "Client Version: v1.2.3") {
		t.Fatalf("missing Client Version line, got:\n%s", got)
	}
	// Boss requirement: never silently drop the Server line — show why it
	// is unavailable so users understand whether they need to log in or
	// whether the server is unreachable.
	if !strings.Contains(got, "Server Version:") {
		t.Fatalf("Server Version line should still render when server fetch fails, got:\n%s", got)
	}
	if !strings.Contains(got, "not logged in") {
		t.Fatalf("expected error reason in output, got:\n%s", got)
	}
}

func TestRenderVersion_JSON_ServerOK(t *testing.T) {
	var buf bytes.Buffer
	renderVersion(&buf, "v1.2.3", "v9.8.7", nil, true)

	var out map[string]string
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if out["client_version"] != "v1.2.3" {
		t.Fatalf("client_version: want v1.2.3, got %q", out["client_version"])
	}
	if out["server_version"] != "v9.8.7" {
		t.Fatalf("server_version: want v9.8.7, got %q", out["server_version"])
	}
	if _, hasErr := out["server_error"]; hasErr {
		t.Fatalf("server_error should be absent when server fetch succeeded, got %q", out["server_error"])
	}
}

func TestRenderVersion_JSON_ServerFailure(t *testing.T) {
	var buf bytes.Buffer
	renderVersion(&buf, "v1.2.3", "", errors.New("connection refused"), true)

	var out map[string]string
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if out["client_version"] != "v1.2.3" {
		t.Fatalf("client_version: want v1.2.3, got %q", out["client_version"])
	}
	if out["server_error"] != "connection refused" {
		t.Fatalf("server_error: want \"connection refused\", got %q", out["server_error"])
	}
	if _, hasVer := out["server_version"]; hasVer {
		t.Fatalf("server_version should be absent on failure, got %q", out["server_version"])
	}
}
