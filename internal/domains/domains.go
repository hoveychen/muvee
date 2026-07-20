// Package domains resolves which platform base domain an inbound request
// belongs to when muvee is served under more than one base domain.
//
// muvee historically assumed a single BASE_DOMAIN: the control-plane panel,
// every project subdomain, auth cookies and OAuth callbacks were all built
// from that one value. To serve the same instance under several apex domains
// (e.g. an overseas domain and a mainland/ICP domain) callers set BASE_DOMAINS
// to a comma-separated list; per request they resolve the matching base domain
// with Match so the cookie Domain and the OAuth redirect land on whatever
// domain the user is actually on.
package domains

import "strings"

// Parse builds the ordered, de-duplicated list of platform base domains from
// the canonical BASE_DOMAIN and the optional comma-separated BASE_DOMAINS.
// The canonical base domain (when non-empty) is always first, so callers that
// need a single default with no request in hand can use the first element.
// All entries are lowercased and trimmed; empty entries and duplicates drop.
func Parse(baseDomain, baseDomains string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(d string) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	add(baseDomain)
	for _, d := range strings.Split(baseDomains, ",") {
		add(d)
	}
	return out
}

// Match returns the configured base domain that host belongs to — either host
// equals the base domain, or host is a subdomain of it (`x.<base>`). When more
// than one base matches (e.g. one base is itself a subdomain of another) the
// longest, most specific base wins. host may carry a :port, which is stripped.
// ok is false when no configured base matches.
func Match(host string, bases []string) (base string, ok bool) {
	host = NormalizeHost(host)
	if host == "" {
		return "", false
	}
	best := ""
	for _, b := range bases {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == "" {
			continue
		}
		if host == b || strings.HasSuffix(host, "."+b) {
			if len(b) > len(best) {
				best = b
			}
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// NormalizeHost lowercases host, trims surrounding space and a trailing dot,
// and strips a trailing :port (leaving bracketed IPv6 literals intact).
func NormalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	return host
}
