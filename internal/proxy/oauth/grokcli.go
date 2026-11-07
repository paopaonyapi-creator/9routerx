// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package oauth

import (
	"context"
	"net/url"
)

func init() {
	Register("grok-cli", refreshGrokCLI)
}

// refreshGrokCLI refreshes a Grok CLI OAuth token.
// Uses same xAI public client (no client_secret needed).
func refreshGrokCLI(ctx context.Context, p *Params) (*TokenResult, error) {
	vals := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {xaiClientID},
		"refresh_token": {p.RefreshToken},
	}
	return doFormRefresh(ctx, p.Client, "https://auth.x.ai/oauth2/token", vals)
}
