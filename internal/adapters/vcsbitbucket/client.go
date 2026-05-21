package vcsbitbucket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// apiClient wraps http.Client with the Bitbucket OAuth bearer token
// applied to every request. baseURL is the v2 root, e.g.
// https://api.bitbucket.org/2.0
type apiClient struct {
	http    *http.Client
	tokens  *tokenSource
	baseURL string
}

func newAPIClient(baseURL string, tokens *tokenSource, hc *http.Client) *apiClient {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &apiClient{http: hc, tokens: tokens, baseURL: baseURL}
}

// getJSON executes GET path (relative to baseURL) and decodes the JSON
// response body into dst. Returns the raw HTTP status code so callers
// can distinguish 404 / 401 / 429.
func (c *apiClient) getJSON(ctx context.Context, path string, dst any) (int, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "application/json")
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// getRaw fetches a path expected to return non-JSON (e.g. diff text).
func (c *apiClient) getRaw(ctx context.Context, path, accept string) ([]byte, int, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, accept)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// postJSON marshals body to JSON, POSTs it, and decodes the response
// into dst (if non-nil).
func (c *apiClient) postJSON(ctx context.Context, path string, body, dst any) (int, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return 0, fmt.Errorf("encode body: %w", err)
		}
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, &buf, "application/json")
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	} else if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return resp.StatusCode, fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, errBody)
	}
	return resp.StatusCode, nil
}

func (c *apiClient) newRequest(ctx context.Context, method, path string, body io.Reader, accept string) (*http.Request, error) {
	full := c.baseURL + path
	if _, err := url.Parse(full); err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, err
	}
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req, nil
}
