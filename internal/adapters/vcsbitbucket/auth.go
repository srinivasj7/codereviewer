package vcsbitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const tokenURL = "https://bitbucket.org/site/oauth2/access_token"

// tokenSource fetches OAuth 2.0 client-credentials grant tokens from
// Bitbucket Cloud and caches them in memory until just before expiry.
// Single shared instance per Source; the mutex protects the cache.
type tokenSource struct {
	clientId     string
	clientSecret string
	http         *http.Client

	mu  sync.Mutex
	tok string
	exp time.Time
}

func newTokenSource(clientId, clientSecret string, client *http.Client) *tokenSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &tokenSource{
		clientId:     clientId,
		clientSecret: clientSecret,
		http:         client,
	}
}

// Token returns a non-expired access token, fetching a new one if the
// cached value is within 60 seconds of expiry.
func (t *tokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tok != "" && time.Until(t.exp) > 60*time.Second {
		return t.tok, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(t.clientId, t.clientSecret)

	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Bitbucket returns JSON like {"error":"invalid_client","error_description":"..."}.
		// Surface up to 256 bytes so the operator can tell apart "private
		// consumer not checked" (invalid_grant) from credential typos
		// (invalid_client) from scope issues.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("token exchange: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token exchange: empty access_token")
	}
	t.tok = out.AccessToken
	if out.ExpiresIn > 0 {
		t.exp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	} else {
		t.exp = time.Now().Add(2 * time.Hour) // Bitbucket default
	}
	return t.tok, nil
}
