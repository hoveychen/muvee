package auth

import (
	"fmt"
	"strings"
)

// NewForwardAuthProviders initialises all OAuth providers for which the
// required environment variables are set. redirectBase is the public base URL
// of the ForwardAuth sidecar (e.g. "https://example.com"); each provider's
// callback URL is computed as "{redirectBase}/_oauth/{provider}".
func NewForwardAuthProviders(redirectBase string) (map[string]Provider, error) {
	providers := make(map[string]Provider)

	googleP, err := newGoogleProvider(redirectBase + "/_oauth/google")
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	if googleP != nil {
		providers[googleP.Name()] = googleP
	}

	feishuP, err := newFeishuProvider(redirectBase + "/_oauth/feishu")
	if err != nil {
		return nil, fmt.Errorf("feishu: %w", err)
	}
	if feishuP != nil {
		providers[feishuP.Name()] = feishuP
	}

	wecomP, err := newWeComProvider(redirectBase + "/_oauth/wecom")
	if err != nil {
		return nil, fmt.Errorf("wecom: %w", err)
	}
	if wecomP != nil {
		providers[wecomP.Name()] = wecomP
	}

	dingtalkP, err := newDingTalkProvider(redirectBase + "/_oauth/dingtalk")
	if err != nil {
		return nil, fmt.Errorf("dingtalk: %w", err)
	}
	if dingtalkP != nil {
		providers[dingtalkP.Name()] = dingtalkP
	}

	slackP, err := newSlackProvider(redirectBase + "/_oauth/slack")
	if err != nil {
		return nil, fmt.Errorf("slack: %w", err)
	}
	if slackP != nil {
		providers[slackP.Name()] = slackP
	}

	return providers, nil
}

// ASCIIHeaderValue makes a value safe to put in an HTTP header without killing
// the upstream.
//
// RFC 9110 field values are ASCII; a non-ASCII byte is deprecated obs-text that
// a server may reject — and strict ones do, at the parser, before any
// application code runs. Observed 2026-08-24: fleet-cloud 502'd on every
// request *after* a successful Feishu login, because the display name forwarded
// in `X-Forwarded-User-Name` is Chinese and its upstream (`tiny_http`) requires
// ASCII header values (`AsciiString::from_ascii`). The container was healthy
// throughout; the request simply never arrived. Isolated on the same backend
// and path: no header → 200, ASCII value → 200, any byte > 0x7F → connection
// dropped with no response and nothing in the application's log.
//
// A value that is already ASCII comes back byte-identical, so every downstream
// that works today keeps seeing exactly what it saw. Only a value that would
// otherwise break the request is percent-encoded — and then `%` is escaped too,
// so the result decodes unambiguously (`url.QueryUnescape` /
// `decodeURIComponent` round-trip it). A downstream that does not decode shows
// `%E9%99%88` instead of the name: degraded, but the page loads.
//
// Both surfaces that forward this identity must use it: the forward-auth
// response headers (authservice) and the project proxy's request headers
// (muvee-server). Fixing one and not the other just moves the 502.
func ASCIIHeaderValue(v string) string {
	needsEncoding := false
	for i := 0; i < len(v); i++ {
		if v[i] > 0x7f {
			needsEncoding = true
			break
		}
	}
	if !needsEncoding {
		return v
	}
	var b strings.Builder
	b.Grow(len(v) + 8)
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c > 0x7f || c == '%' {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
