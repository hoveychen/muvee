package auth

import "fmt"

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

	return providers, nil
}
