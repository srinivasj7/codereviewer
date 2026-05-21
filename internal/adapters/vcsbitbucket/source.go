// Package vcsbitbucket is the Bitbucket Cloud VcsSource adapter.
//
// Auth: OAuth 2.0 client-credentials grant against a workspace-level
// OAuth consumer. The bearer token is cached in-memory and refreshed
// 60s before expiry.
//
// RepoId convention: "<workspace>/<slug>" (Bitbucket's "full_name"
// format). Webhook signatures use the X-Hub-Signature header with
// "sha256=<hex>" — the same convention as GitHub since 2024, so the
// HMAC code mirrors vcsgithub.
//
// Bitbucket has no batch "submit review" API like GitHub: each inline
// comment is its own POST. PostReview therefore loops, recording
// partial success in the returned CommentIds slice.
package vcsbitbucket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

const defaultBaseURL = "https://api.bitbucket.org/2.0"

// Source implements ports.VcsSource against Bitbucket Cloud.
type Source struct {
	api           *apiClient
	webhookSecret []byte
}

// New constructs a Source.
func New(cfg schemas.VcsConfig) (*Source, error) {
	if cfg.BitbucketClientId == "" {
		return nil, fmt.Errorf("vcsbitbucket: bitbucket_client_id is required")
	}
	if cfg.BitbucketClientSecret == "" {
		return nil, fmt.Errorf("vcsbitbucket: bitbucket_client_secret is required")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("vcsbitbucket: webhook_secret is required")
	}
	baseURL := cfg.BitbucketBaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	tokens := newTokenSource(cfg.BitbucketClientId, cfg.BitbucketClientSecret, hc)
	return &Source{
		api:           newAPIClient(baseURL, tokens, hc),
		webhookSecret: []byte(cfg.WebhookSecret),
	}, nil
}

// VerifyWebhook checks HMAC + maps to a canonical WebhookEvent.
func (s *Source) VerifyWebhook(_ context.Context, headers http.Header, rawBody []byte) (ports.WebhookEvent, error) {
	if err := verifySignature(headers, rawBody, s.webhookSecret); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("invalid webhook signature: %w", err)
	}
	return parseEvent(headers, rawBody)
}

// FetchDiff returns the unified diff for a PR.
func (s *Source) FetchDiff(ctx context.Context, ref ports.PrRef) (ports.UnifiedDiff, error) {
	ws, slug, err := parseRepoId(ref.RepoId)
	if err != nil {
		return ports.UnifiedDiff{}, err
	}
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/diff", ws, slug, ref.PrNumber)
	body, status, err := s.api.getRaw(ctx, path, "text/plain")
	if err != nil {
		return ports.UnifiedDiff{}, fmt.Errorf("fetch diff: %w", err)
	}
	if status != http.StatusOK {
		return ports.UnifiedDiff{}, fmt.Errorf("fetch diff: status %d", status)
	}
	return ports.UnifiedDiff{HeadSha: ref.HeadSha, Content: string(body)}, nil
}

// FetchDiffBetween returns the unified diff between two commits via
// Bitbucket's /diff/{spec} endpoint. spec format is "<head>..<base>"
// (Bitbucket's convention has head first).
func (s *Source) FetchDiffBetween(ctx context.Context, repoId ports.RepoId, baseSha, headSha string) (ports.UnifiedDiff, error) {
	ws, slug, err := parseRepoId(repoId)
	if err != nil {
		return ports.UnifiedDiff{}, err
	}
	path := fmt.Sprintf("/repositories/%s/%s/diff/%s..%s", ws, slug, headSha, baseSha)
	body, status, err := s.api.getRaw(ctx, path, "text/plain")
	if err != nil {
		return ports.UnifiedDiff{}, fmt.Errorf("fetch compare diff: %w", err)
	}
	if status != http.StatusOK {
		return ports.UnifiedDiff{}, fmt.Errorf("fetch compare diff: status %d", status)
	}
	return ports.UnifiedDiff{
		BaseSha: baseSha,
		HeadSha: headSha,
		Content: string(body),
	}, nil
}

// FetchPrMeta returns title, body, and source branch name.
func (s *Source) FetchPrMeta(ctx context.Context, ref ports.PrRef) (ports.PrMeta, error) {
	ws, slug, err := parseRepoId(ref.RepoId)
	if err != nil {
		return ports.PrMeta{}, err
	}
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d", ws, slug, ref.PrNumber)
	var resp struct {
		Title       string       `json:"title"`
		Description string       `json:"description"`
		Source      bbBranchSide `json:"source"`
	}
	status, err := s.api.getJSON(ctx, path, &resp)
	if err != nil {
		return ports.PrMeta{}, fmt.Errorf("fetch pr meta: %w", err)
	}
	if status != http.StatusOK {
		return ports.PrMeta{}, fmt.Errorf("fetch pr meta: status %d", status)
	}
	return ports.PrMeta{
		Title:      resp.Title,
		Body:       resp.Description,
		BranchName: resp.Source.Branch.Name,
	}, nil
}

// FetchFileAt fetches file contents at a specific commit.
func (s *Source) FetchFileAt(ctx context.Context, repoId ports.RepoId, sha, filePath string) (string, error) {
	ws, slug, err := parseRepoId(repoId)
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("/repositories/%s/%s/src/%s/%s",
		ws, slug, url.PathEscape(sha), filePath)
	body, status, err := s.api.getRaw(ctx, path, "text/plain")
	if err != nil {
		return "", fmt.Errorf("fetch file: %w", err)
	}
	if status == http.StatusNotFound {
		return "", fmt.Errorf("%s not found at %s", filePath, sha)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("fetch file: status %d", status)
	}
	return string(body), nil
}

// ListChangedFiles uses diffstat between two commits.
func (s *Source) ListChangedFiles(ctx context.Context, repoId ports.RepoId, baseSha, headSha string) ([]ports.ChangedFile, error) {
	ws, slug, err := parseRepoId(repoId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/repositories/%s/%s/diffstat/%s..%s?pagelen=100",
		ws, slug, headSha, baseSha)
	var out []ports.ChangedFile
	for {
		var page struct {
			Values []struct {
				Status string `json:"status"`
				New    *struct {
					Path string `json:"path"`
				} `json:"new"`
				Old *struct {
					Path string `json:"path"`
				} `json:"old"`
			} `json:"values"`
			Next string `json:"next"`
		}
		status, err := s.api.getJSON(ctx, path, &page)
		if err != nil {
			return nil, fmt.Errorf("diffstat: %w", err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("diffstat: status %d", status)
		}
		for _, v := range page.Values {
			file := ""
			if v.New != nil {
				file = v.New.Path
			} else if v.Old != nil {
				file = v.Old.Path
			}
			if file == "" {
				continue
			}
			out = append(out, ports.ChangedFile{Path: file, Status: v.Status})
		}
		if page.Next == "" {
			return out, nil
		}
		// next is an absolute URL; trim baseURL prefix to keep apiClient happy.
		path = strings.TrimPrefix(page.Next, s.api.baseURL)
	}
}

// ListPrComments returns review-level comments. Reactions aren't
// surfaced by Bitbucket on comments, so ReactionsPlusOne/MinusOne are
// always zero — the feedback worker treats absence as "no signal".
func (s *Source) ListPrComments(ctx context.Context, ref ports.PrRef) ([]ports.HumanComment, error) {
	ws, slug, err := parseRepoId(ref.RepoId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments?pagelen=100",
		ws, slug, ref.PrNumber)
	var out []ports.HumanComment
	for {
		var page struct {
			Values []struct {
				Id        int64  `json:"id"`
				CreatedOn string `json:"created_on"`
				Content   struct {
					Raw string `json:"raw"`
				} `json:"content"`
				User   bbActor          `json:"user"`
				Inline *bbCommentInline `json:"inline"`
			} `json:"values"`
			Next string `json:"next"`
		}
		status, err := s.api.getJSON(ctx, path, &page)
		if err != nil {
			return nil, fmt.Errorf("list comments: %w", err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("list comments: status %d", status)
		}
		for _, v := range page.Values {
			c := ports.HumanComment{
				ExternalId: v.Id,
				Author:     nicknameOr(v.User),
				Body:       v.Content.Raw,
			}
			if v.Inline != nil {
				c.Path = v.Inline.Path
				if v.Inline.To != nil {
					c.EndLine = *v.Inline.To
				}
				if v.Inline.From != nil {
					c.StartLine = *v.Inline.From
				}
			}
			if v.CreatedOn != "" {
				if t, err := time.Parse(time.RFC3339, v.CreatedOn); err == nil {
					c.CreatedAt = t
				}
			}
			out = append(out, c)
		}
		if page.Next == "" {
			return out, nil
		}
		path = strings.TrimPrefix(page.Next, s.api.baseURL)
	}
}

// SearchClosedPrs enumerates merged PRs updated on or after `since`.
func (s *Source) SearchClosedPrs(ctx context.Context, repoId ports.RepoId, since time.Time) ([]int, error) {
	ws, slug, err := parseRepoId(repoId)
	if err != nil {
		return nil, err
	}
	q := url.QueryEscape(fmt.Sprintf(`state="MERGED" AND updated_on>=%s`,
		since.UTC().Format("2006-01-02T15:04:05Z")))
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests?q=%s&pagelen=50&fields=values.id,next",
		ws, slug, q)
	var prs []int
	for {
		var page struct {
			Values []struct {
				Id int `json:"id"`
			} `json:"values"`
			Next string `json:"next"`
		}
		status, err := s.api.getJSON(ctx, path, &page)
		if err != nil {
			return nil, fmt.Errorf("search prs: %w", err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("search prs: status %d", status)
		}
		for _, v := range page.Values {
			prs = append(prs, v.Id)
		}
		if page.Next == "" {
			return prs, nil
		}
		path = strings.TrimPrefix(page.Next, s.api.baseURL)
	}
}

// PostReview submits inline comments + a summary comment. Bitbucket
// has no batch review API, so each inline lands in its own POST.
// A single inline failure does NOT abort the rest; the returned
// CommentIds list reports the survivors in order.
func (s *Source) PostReview(ctx context.Context, ref ports.PrRef, review ports.ReviewPayload) (ports.PostedReview, error) {
	ws, slug, err := parseRepoId(ref.RepoId)
	if err != nil {
		return ports.PostedReview{}, err
	}
	base := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", ws, slug, ref.PrNumber)

	var inlineIds []int64
	for _, c := range review.Comments {
		body := map[string]any{
			"content": map[string]any{"raw": c.Body},
			"inline":  map[string]any{"path": c.File, "to": c.EndLine},
		}
		var resp struct {
			Id int64 `json:"id"`
		}
		status, err := s.api.postJSON(ctx, base, body, &resp)
		if err != nil || (status < 200 || status >= 300) {
			// Don't abort the batch — partial-success is acceptable.
			continue
		}
		inlineIds = append(inlineIds, resp.Id)
	}

	// Summary comment (no inline location).
	var summaryResp struct {
		Id int64 `json:"id"`
	}
	summary := map[string]any{"content": map[string]any{"raw": review.Body}}
	status, err := s.api.postJSON(ctx, base, summary, &summaryResp)
	if err != nil {
		return ports.PostedReview{}, fmt.Errorf("post summary: %w", err)
	}
	if status < 200 || status >= 300 {
		return ports.PostedReview{}, fmt.Errorf("post summary: status %d", status)
	}

	return ports.PostedReview{
		ReviewId:   summaryResp.Id,
		PostedAt:   time.Now(),
		CommentIds: inlineIds,
	}, nil
}

// UpdateCheck writes a build status to the head commit. Bitbucket
// states: SUCCESSFUL | FAILED | INPROGRESS | STOPPED. We map our
// {success, failure, neutral, timed_out} into the closest fit:
//
//	success / neutral -> SUCCESSFUL
//	failure           -> FAILED
//	timed_out         -> STOPPED
func (s *Source) UpdateCheck(ctx context.Context, ref ports.PrRef, result ports.CheckResult) error {
	ws, slug, err := parseRepoId(ref.RepoId)
	if err != nil {
		return err
	}
	state := mapConclusion(result.Conclusion)
	// Bitbucket's build-status API requires `url` even when there's no
	// per-run details page. Fall back to a stable per-repo URL so the
	// API accepts the call.
	detailsURL := result.DetailsURL
	if detailsURL == "" {
		detailsURL = fmt.Sprintf("https://bitbucket.org/%s/%s/commits/%s", ws, slug, ref.HeadSha)
	}
	body := map[string]any{
		"state":       state,
		"key":         result.Name,
		"name":        result.Name,
		"description": truncate(result.Summary, 140),
		"url":         detailsURL,
	}
	path := fmt.Sprintf("/repositories/%s/%s/commit/%s/statuses/build", ws, slug, ref.HeadSha)
	status, err := s.api.postJSON(ctx, path, body, nil)
	if err != nil {
		return fmt.Errorf("update check: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("update check: status %d", status)
	}
	return nil
}

func mapConclusion(c string) string {
	switch c {
	case "failure":
		return "FAILED"
	case "timed_out":
		return "STOPPED"
	}
	// success, neutral, anything else -> SUCCESSFUL
	return "SUCCESSFUL"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func parseRepoId(id ports.RepoId) (workspace, slug string, err error) {
	parts := strings.SplitN(string(id), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo_id %q (expected workspace/slug)", id)
	}
	return parts[0], parts[1], nil
}
