// Package contextlinear is a ContextProvider that fetches Linear
// issue titles + descriptions referenced from a PR's title, branch,
// or body. Auth: personal API key in the `Authorization` header.
//
// Linear issue keys follow the same TEAM-123 shape as JIRA. To avoid
// pinging Linear for stray JIRA-style keys, callers can configure
// `team_prefixes` — keys whose prefix isn't in the allow-list are
// skipped before the API call.
package contextlinear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codereviewer/internal/adapters/contextissues"
	"codereviewer/internal/ports"
)

// Provider implements ports.ContextProvider against Linear's GraphQL API.
type Provider struct {
	apiKey   string
	prefixes []string // upper-case team prefixes; empty = accept all
	vcs      ports.VcsSource
	http     *http.Client
	obs      ports.Obs
}

// New constructs a Provider. teamPrefixes may be nil/empty to accept
// any JIRA-style key.
func New(apiKey string, teamPrefixes []string, vcs ports.VcsSource, obs ports.Obs) *Provider {
	up := make([]string, len(teamPrefixes))
	for i, p := range teamPrefixes {
		up[i] = strings.ToUpper(strings.TrimSpace(p))
	}
	return &Provider{
		apiKey:   apiKey,
		prefixes: up,
		vcs:      vcs,
		http:     &http.Client{Timeout: 10 * time.Second},
		obs:      obs,
	}
}

// Name implements ports.ContextProvider.
func (p *Provider) Name() string { return "linear" }

// Fetch implements ports.ContextProvider.
func (p *Provider) Fetch(ctx context.Context, ref ports.PrRef) ([]ports.ContextItem, error) {
	meta, err := p.vcs.FetchPrMeta(ctx, ref)
	if err != nil {
		p.obs.Logger.Warn("linear: pr meta failed", "err", err.Error())
		return nil, nil
	}
	keys := contextissues.JiraStyleKeys(meta.Title, meta.BranchName, meta.Body)
	if len(keys) == 0 {
		return nil, nil
	}
	var items []ports.ContextItem
	for _, k := range keys {
		if !p.acceptKey(k) {
			continue
		}
		title, body, ok := p.fetchIssue(ctx, k)
		if !ok {
			continue
		}
		items = append(items, ports.ContextItem{
			Source:   p.Name(),
			Title:    k + " — " + title,
			Body:     body,
			Priority: 60,
		})
	}
	return items, nil
}

// acceptKey enforces the team_prefixes allow-list when configured.
// The check is on the team prefix only (everything before the dash).
func (p *Provider) acceptKey(key string) bool {
	if len(p.prefixes) == 0 {
		return true
	}
	dash := strings.IndexByte(key, '-')
	if dash <= 0 {
		return false
	}
	prefix := strings.ToUpper(key[:dash])
	for _, a := range p.prefixes {
		if a == prefix {
			return true
		}
	}
	return false
}

// fetchIssue runs the GraphQL query. Linear's `issue` accepts the
// human-readable identifier (e.g. ENG-123) directly.
func (p *Provider) fetchIssue(ctx context.Context, key string) (title, body string, ok bool) {
	const query = `query($id: String!) { issue(id: $id) { title description } }`
	reqBody, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]string{"id": key},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.linear.app/graphql", bytes.NewReader(reqBody))
	if err != nil {
		return "", "", false
	}
	req.Header.Set("Authorization", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		p.obs.Logger.Warn("linear: fetch failed", "key", key, "err", err.Error())
		return "", "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", false
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", "", false
	}
	var payload struct {
		Data struct {
			Issue struct {
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", false
	}
	if payload.Data.Issue.Title == "" {
		return "", "", false
	}
	return payload.Data.Issue.Title, payload.Data.Issue.Description, true
}

// Compile-time check.
var _ = fmt.Sprintf
