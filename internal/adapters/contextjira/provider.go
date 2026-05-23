// Package contextjira is a ContextProvider that fetches summaries +
// descriptions of JIRA issues referenced from a PR's title, branch,
// or body. Auth: email + API token (Atlassian standard).
//
// Provider errors and per-issue 404s are absorbed silently — a broken
// JIRA must not fail the review.
package contextjira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codereviewer/internal/adapters/contextissues"
	"codereviewer/internal/ports"
)

// Provider implements ports.ContextProvider against Atlassian JIRA Cloud.
type Provider struct {
	baseURL    string
	authHeader string
	vcs        ports.VcsRegistry
	http       *http.Client
	obs        ports.Obs
}

// New constructs a Provider. baseURL is the JIRA site (e.g.
// https://acme.atlassian.net). vcs is used only to fetch PR meta; the
// registry is resolved per-ref so the provider works across VCSes.
func New(baseURL, email, apiToken string, vcs ports.VcsRegistry, obs ports.Obs) *Provider {
	creds := base64.StdEncoding.EncodeToString([]byte(email + ":" + apiToken))
	return &Provider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: "Basic " + creds,
		vcs:        vcs,
		http:       &http.Client{Timeout: 10 * time.Second},
		obs:        obs,
	}
}

// Name implements ports.ContextProvider.
func (p *Provider) Name() string { return "jira" }

// Fetch implements ports.ContextProvider.
func (p *Provider) Fetch(ctx context.Context, ref ports.PrRef) ([]ports.ContextItem, error) {
	vcs, err := p.vcs.For(ref.ProviderOrDefault())
	if err != nil {
		p.obs.Logger.Warn("jira: vcs registry lookup failed",
			"provider", string(ref.ProviderOrDefault()), "err", err.Error())
		return nil, nil
	}
	meta, err := vcs.FetchPrMeta(ctx, ref)
	if err != nil {
		p.obs.Logger.Warn("jira: pr meta failed", "err", err.Error())
		return nil, nil
	}
	keys := contextissues.JiraStyleKeys(meta.Title, meta.BranchName, meta.Body)
	if len(keys) == 0 {
		return nil, nil
	}
	var items []ports.ContextItem
	for _, k := range keys {
		issue, ok := p.fetchIssue(ctx, k)
		if !ok {
			continue
		}
		items = append(items, ports.ContextItem{
			Source:   p.Name(),
			Title:    k + " — " + issue.Summary,
			Body:     issue.Description,
			Priority: 60,
		})
	}
	return items, nil
}

type jiraIssue struct {
	Summary     string
	Description string
}

// fetchIssue returns the issue from JIRA's REST API. Returns ok=false
// on any error so the rest of the provider's keys can still resolve.
func (p *Provider) fetchIssue(ctx context.Context, key string) (jiraIssue, bool) {
	u := p.baseURL + "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=summary,description"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return jiraIssue{}, false
	}
	req.Header.Set("Authorization", p.authHeader)
	req.Header.Set("Accept", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		p.obs.Logger.Warn("jira: fetch failed", "key", key, "err", err.Error())
		return jiraIssue{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// 401 / 403 / 404 are routine — the caller's PR text may
		// mention keys from other JIRA sites or deleted issues.
		return jiraIssue{}, false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return jiraIssue{}, false
	}
	var payload struct {
		Fields struct {
			Summary     string `json:"summary"`
			Description any    `json:"description"` // ADF doc or string depending on API
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return jiraIssue{}, false
	}
	desc := renderADFText(payload.Fields.Description)
	return jiraIssue{Summary: payload.Fields.Summary, Description: desc}, true
}

// renderADFText extracts plain text from JIRA's Atlassian Document
// Format payload. ADF is a nested tree; we walk it and concatenate
// every `text` leaf. If the field is already a string (older API), we
// return it as-is. Anything unexpected → empty string.
func renderADFText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any:
		var b strings.Builder
		walkADF(x, &b)
		return strings.TrimSpace(b.String())
	}
	return ""
}

func walkADF(node map[string]any, b *strings.Builder) {
	if t, ok := node["text"].(string); ok {
		b.WriteString(t)
	}
	if content, ok := node["content"].([]any); ok {
		for _, c := range content {
			if m, ok := c.(map[string]any); ok {
				walkADF(m, b)
			}
		}
		// Paragraph-like containers separate logical blocks.
		if nt, _ := node["type"].(string); nt == "paragraph" || nt == "heading" || nt == "listItem" {
			b.WriteByte('\n')
		}
	}
}

// Compile-time check.
var _ = fmt.Sprintf
