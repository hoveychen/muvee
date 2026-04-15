package api

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

// CertStatus is the ACME state of one expected HTTPS domain.
type CertStatus struct {
	Domain    string     `json:"domain"`
	Kind      string     `json:"kind"`   // base|registry|traefik|project|tunnel
	Status    string     `json:"status"` // issued|pending|unknown
	NotAfter  *time.Time `json:"not_after,omitempty"`
	DaysLeft  *int       `json:"days_left,omitempty"`
	Issuer    string     `json:"issuer,omitempty"`
	Message   string     `json:"message,omitempty"`
}

// CertReport is the full set of domains muvee expects to be served over HTTPS,
// each annotated with its current ACME state from Traefik's acme.json store.
type CertReport struct {
	StorePath  string       `json:"store_path"`
	StoreError string       `json:"store_error,omitempty"`
	Items      []CertStatus `json:"items"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// traefikACMEFile mirrors the on-disk layout of Traefik v3's acme.json, keyed
// by certificate resolver name. We only care about the "letsencrypt" resolver.
type traefikACMEFile map[string]traefikACMEResolver

type traefikACMEResolver struct {
	Certificates []traefikACMECertificate `json:"Certificates"`
}

type traefikACMECertificate struct {
	Domain      traefikACMEDomain `json:"domain"`
	Certificate string            `json:"certificate"` // base64-encoded PEM chain
}

type traefikACMEDomain struct {
	Main string   `json:"main"`
	SANs []string `json:"sans"`
}

// loadACMEStore reads and parses the acme.json file from disk. Returns a map
// from domain (main + SANs, lower-cased) to the parsed leaf certificate so we
// can report issuer and expiry alongside the "issued" flag. A missing file is
// not an error — it just means no certs have been issued yet.
func loadACMEStore(path string) (map[string]*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*x509.Certificate{}, nil
		}
		return nil, err
	}
	var file traefikACMEFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse acme.json: %w", err)
	}
	out := map[string]*x509.Certificate{}
	// Prefer the "letsencrypt" resolver (matches traefik.yml) but fall back to
	// whatever resolver shows up first — useful if someone renamed it.
	resolver, ok := file["letsencrypt"]
	if !ok {
		for _, r := range file {
			resolver = r
			break
		}
	}
	for _, c := range resolver.Certificates {
		leaf := parseLeafCert(c.Certificate)
		for _, d := range append([]string{c.Domain.Main}, c.Domain.SANs...) {
			if d == "" {
				continue
			}
			out[d] = leaf
		}
	}
	return out, nil
}

// parseLeafCert decodes a base64-encoded PEM chain and returns the first
// certificate (the leaf). Returns nil on any error — callers treat that as
// "cert exists but we couldn't read expiry", which is still better than
// reporting the domain as pending.
func parseLeafCert(b64 string) *x509.Certificate {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return cert
}

// expectedDomains enumerates every hostname muvee expects Traefik to serve
// over HTTPS: the control-plane base domain, the fixed registry/traefik
// subdomains, every running project, and every live tunnel. The order here
// drives display order in the admin UI.
func (s *Server) expectedDomains(ctx context.Context) []CertStatus {
	items := []CertStatus{}
	if s.baseDomain == "" {
		return items
	}
	items = append(items,
		CertStatus{Domain: s.baseDomain, Kind: "base"},
		CertStatus{Domain: "registry." + s.baseDomain, Kind: "registry"},
		CertStatus{Domain: "traefik." + s.baseDomain, Kind: "traefik"},
	)
	if deps, err := s.store.GetRunningDeployments(ctx); err == nil {
		for _, d := range deps {
			items = append(items, CertStatus{
				Domain: d.DomainPrefix + "." + s.baseDomain,
				Kind:   "project",
			})
		}
	}
	for _, t := range s.tunnels.activeTunnels() {
		items = append(items, CertStatus{
			Domain: t.Domain + "." + s.baseDomain,
			Kind:   "tunnel",
		})
	}
	return items
}

// handleGetCertificateStatus returns the ACME state of every expected domain.
// Admins use it to tell whether a deploy is actually reachable over HTTPS or
// still waiting for Traefik to finish the ACME challenge.
func (s *Server) handleGetCertificateStatus(w http.ResponseWriter, r *http.Request) {
	path := s.acmeStoragePath
	if path == "" {
		path = "/letsencrypt/acme.json"
	}

	report := CertReport{
		StorePath: path,
		UpdatedAt: time.Now(),
	}

	store, err := loadACMEStore(path)
	if err != nil {
		report.StoreError = err.Error()
	}

	items := s.expectedDomains(r.Context())
	now := time.Now()
	for i := range items {
		if report.StoreError != "" {
			items[i].Status = "unknown"
			items[i].Message = "acme.json unreadable — check the volume mount"
			continue
		}
		leaf, ok := store[items[i].Domain]
		if !ok {
			items[i].Status = "pending"
			items[i].Message = "no certificate issued yet — Traefik will request one on the next config reload or first HTTPS request"
			continue
		}
		items[i].Status = "issued"
		if leaf != nil {
			na := leaf.NotAfter
			items[i].NotAfter = &na
			days := int(na.Sub(now).Hours() / 24)
			items[i].DaysLeft = &days
			items[i].Issuer = leaf.Issuer.CommonName
			if days < 0 {
				items[i].Status = "pending"
				items[i].Message = "certificate expired — Traefik will renew on next request"
			} else if days < 14 {
				items[i].Message = fmt.Sprintf("renews soon (%d day(s) left)", days)
			}
		}
	}

	// Sort: non-issued first (so admins see blockers at the top), then by kind,
	// then by domain.
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := statusRank(items[i].Status), statusRank(items[j].Status)
		if ri != rj {
			return ri < rj
		}
		if items[i].Kind != items[j].Kind {
			return kindRank(items[i].Kind) < kindRank(items[j].Kind)
		}
		return items[i].Domain < items[j].Domain
	})

	report.Items = items
	jsonOK(w, report)
}

func statusRank(s string) int {
	switch s {
	case "unknown":
		return 0
	case "pending":
		return 1
	case "issued":
		return 2
	}
	return 3
}

func kindRank(k string) int {
	switch k {
	case "base":
		return 0
	case "registry":
		return 1
	case "traefik":
		return 2
	case "project":
		return 3
	case "tunnel":
		return 4
	}
	return 5
}
