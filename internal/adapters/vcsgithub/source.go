// Package vcsgithub is the GitHub Cloud VcsSource adapter. It uses
// GitHub App authentication (ghinstallation/v2) to issue per-call
// installation tokens, verifies inbound webhooks via HMAC-SHA256, and
// uses go-github for REST calls.
//
// RepoId convention: "<owner>/<name>". Slice 2 can introduce a more
// abstract repo identifier; for slice 1 the GitHub-shaped string keeps
// the adapter simple.
package vcsgithub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v66/github"

	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

// Source is the GitHub implementation of ports.VcsSource.
type Source struct {
	client        *github.Client
	webhookSecret []byte
}

// New constructs a Source. Either PrivateKey (inline PEM) or
// PrivateKeyPath must be set. InstallationId is the GitHub App
// installation id; multi-installation support is deferred to slice 2.
func New(cfg schemas.VcsConfig) (*Source, error) {
	if cfg.AppId == "" {
		return nil, fmt.Errorf("vcsgithub: app_id is required")
	}
	if cfg.InstallationId == "" {
		return nil, fmt.Errorf("vcsgithub: installation_id is required")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("vcsgithub: webhook_secret is required")
	}

	appID, err := strconv.ParseInt(cfg.AppId, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("vcsgithub: invalid app_id: %w", err)
	}
	installationID, err := strconv.ParseInt(cfg.InstallationId, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("vcsgithub: invalid installation_id: %w", err)
	}

	pemBytes, err := loadPrivateKey(cfg)
	if err != nil {
		return nil, err
	}
	itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, pemBytes)
	if err != nil {
		return nil, fmt.Errorf("vcsgithub: install transport: %w", err)
	}

	client := github.NewClient(&http.Client{
		Transport: itr,
		Timeout:   30 * time.Second,
	})
	return &Source{
		client:        client,
		webhookSecret: []byte(cfg.WebhookSecret),
	}, nil
}

func loadPrivateKey(cfg schemas.VcsConfig) ([]byte, error) {
	if cfg.PrivateKeyPath != "" {
		b, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("vcsgithub: read private key: %w", err)
		}
		return b, nil
	}
	if cfg.PrivateKey != "" {
		return []byte(cfg.PrivateKey), nil
	}
	return nil, fmt.Errorf("vcsgithub: private_key or private_key_path must be set")
}

// VerifyWebhook checks the HMAC signature and parses the inbound event
// into a WebhookEvent. Returns an error if the signature is invalid or
// the event type is unsupported.
func (s *Source) VerifyWebhook(_ context.Context, headers http.Header, rawBody []byte) (ports.WebhookEvent, error) {
	sig := headers.Get("X-Hub-Signature-256")
	if sig == "" {
		return ports.WebhookEvent{}, fmt.Errorf("missing X-Hub-Signature-256 header")
	}
	if !verifySignature(s.webhookSecret, rawBody, sig) {
		return ports.WebhookEvent{}, fmt.Errorf("invalid webhook signature")
	}

	deliveryId := headers.Get("X-GitHub-Delivery")
	event := headers.Get("X-GitHub-Event")
	switch event {
	case "pull_request":
		return parsePullRequest(deliveryId, rawBody)
	case "push":
		return parsePush(deliveryId, rawBody)
	case "pull_request_review_comment":
		return parseReviewComment(deliveryId, rawBody)
	case "reaction":
		return parseReaction(deliveryId, rawBody)
	case "ping":
		// GitHub sends a ping on initial registration; treat as no-op.
		return ports.WebhookEvent{Kind: "ping", DeliveryId: deliveryId}, nil
	}
	return ports.WebhookEvent{}, fmt.Errorf("unsupported webhook event %q", event)
}

func verifySignature(secret, body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func parsePullRequest(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p schemas.GithubPullRequestEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse pull_request: %w", err)
	}
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindPullRequest,
		DeliveryId: deliveryId,
		PullRequest: &ports.PullRequestPayload{
			Action: p.Action,
			Ref: ports.PrRef{
				RepoId:   ports.RepoId(p.Repository.Owner.Login + "/" + p.Repository.Name),
				PrNumber: p.PullRequest.Number,
				HeadSha:  p.PullRequest.Head.Sha,
			},
			Repo: ports.RepoRef{
				RepoId:        ports.RepoId(p.Repository.Owner.Login + "/" + p.Repository.Name),
				Owner:         p.Repository.Owner.Login,
				Name:          p.Repository.Name,
				DefaultBranch: p.Repository.DefaultBranch,
			},
			BaseSha: p.PullRequest.Base.Sha,
			IsDraft: p.PullRequest.Draft,
		},
	}, nil
}

func parsePush(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p schemas.GithubPushEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse push: %w", err)
	}
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindPush,
		DeliveryId: deliveryId,
		Push: &ports.PushPayload{
			Repo: ports.RepoRef{
				RepoId:        ports.RepoId(p.Repository.Owner.Login + "/" + p.Repository.Name),
				Owner:         p.Repository.Owner.Login,
				Name:          p.Repository.Name,
				DefaultBranch: p.Repository.DefaultBranch,
			},
			Ref:       p.Ref,
			BeforeSha: p.Before,
			HeadSha:   p.After,
		},
	}, nil
}

func parseReviewComment(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p schemas.GithubReviewCommentEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse review_comment: %w", err)
	}
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindReviewComment,
		DeliveryId: deliveryId,
		ReviewComment: &ports.ReviewCommentPayload{
			Ref: ports.PrRef{
				RepoId:   ports.RepoId(p.Repository.Owner.Login + "/" + p.Repository.Name),
				PrNumber: p.PullRequest.Number,
				HeadSha:  p.PullRequest.Head.Sha,
			},
			CommentId:   p.Comment.Id,
			AuthorId:    p.Comment.User.Login,
			Body:        p.Comment.Body,
			IsBot:       p.Comment.User.Type == "Bot",
			InReplyToId: p.Comment.InReplyToId,
		},
	}, nil
}

func parseReaction(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p schemas.GithubReactionEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse reaction: %w", err)
	}
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindReaction,
		DeliveryId: deliveryId,
		Reaction: &ports.ReactionPayload{
			CommentExternalId: p.Comment.Id,
			Reaction:          p.Reaction.Content,
			UserId:            p.Reaction.User.Login,
		},
	}, nil
}

// FetchDiff retrieves the unified diff for a PR via the
// application/vnd.github.diff media type.
func (s *Source) FetchDiff(ctx context.Context, ref ports.PrRef) (ports.UnifiedDiff, error) {
	owner, name, err := parseRepoId(ref.RepoId)
	if err != nil {
		return ports.UnifiedDiff{}, err
	}
	diff, _, err := s.client.PullRequests.GetRaw(ctx, owner, name, ref.PrNumber, github.RawOptions{Type: github.Diff})
	if err != nil {
		return ports.UnifiedDiff{}, fmt.Errorf("fetch diff: %w", err)
	}
	return ports.UnifiedDiff{
		HeadSha: ref.HeadSha,
		Content: diff,
		Files:   nil, // parsed by indexer when needed; review pipeline uses Content directly
	}, nil
}

// FetchFileAt retrieves one file's contents at a specific sha.
func (s *Source) FetchFileAt(ctx context.Context, repoId ports.RepoId, sha, path string) (string, error) {
	owner, name, err := parseRepoId(repoId)
	if err != nil {
		return "", err
	}
	content, _, _, err := s.client.Repositories.GetContents(ctx, owner, name, path, &github.RepositoryContentGetOptions{Ref: sha})
	if err != nil {
		return "", fmt.Errorf("get contents: %w", err)
	}
	if content == nil {
		return "", fmt.Errorf("%s not found at %s", path, sha)
	}
	decoded, err := content.GetContent()
	if err != nil {
		return "", fmt.Errorf("decode content: %w", err)
	}
	return decoded, nil
}

// ListChangedFiles uses the compare API to enumerate files changed
// between baseSha and headSha.
func (s *Source) ListChangedFiles(ctx context.Context, repoId ports.RepoId, baseSha, headSha string) ([]ports.ChangedFile, error) {
	owner, name, err := parseRepoId(repoId)
	if err != nil {
		return nil, err
	}
	cmp, _, err := s.client.Repositories.CompareCommits(ctx, owner, name, baseSha, headSha, nil)
	if err != nil {
		return nil, fmt.Errorf("compare commits: %w", err)
	}
	out := make([]ports.ChangedFile, 0, len(cmp.Files))
	for _, f := range cmp.Files {
		out = append(out, ports.ChangedFile{
			Path:   f.GetFilename(),
			Status: f.GetStatus(),
		})
	}
	return out, nil
}

// ListPrComments returns review-level comments on a PR including diff
// hunks and reactions, both of which are used by the backfill and
// retrieval pipelines.
func (s *Source) ListPrComments(ctx context.Context, ref ports.PrRef) ([]ports.HumanComment, error) {
	owner, name, err := parseRepoId(ref.RepoId)
	if err != nil {
		return nil, err
	}
	var all []ports.HumanComment
	opt := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := s.client.PullRequests.ListComments(ctx, owner, name, ref.PrNumber, opt)
		if err != nil {
			return nil, fmt.Errorf("list pr comments: %w", err)
		}
		for _, c := range comments {
			all = append(all, ports.HumanComment{
				ExternalId:        c.GetID(),
				Author:            c.GetUser().GetLogin(),
				Body:              c.GetBody(),
				Path:              c.GetPath(),
				DiffHunk:          c.GetDiffHunk(),
				CommitId:          c.GetCommitID(),
				StartLine:         c.GetStartLine(),
				EndLine:           c.GetLine(),
				ReactionsPlusOne:  c.GetReactions().GetPlusOne(),
				ReactionsMinusOne: c.GetReactions().GetMinusOne(),
				CreatedAt:         c.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

// SearchClosedPrs enumerates closed PRs in the repo merged on or after
// `since`. Paginates through GitHub Search (max 1000 results per query
// by API design; for windows larger than 1000 PRs the caller must
// narrow the window or this method needs date-range chunking).
func (s *Source) SearchClosedPrs(ctx context.Context, repoId ports.RepoId, since time.Time) ([]int, error) {
	owner, name, err := parseRepoId(repoId)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("repo:%s/%s is:pr is:closed merged:>=%s",
		owner, name, since.UTC().Format("2006-01-02"))
	opt := &github.SearchOptions{
		Sort:        "created",
		Order:       "asc",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var prs []int
	for {
		result, resp, err := s.client.Search.Issues(ctx, query, opt)
		if err != nil {
			return nil, fmt.Errorf("search closed prs: %w", err)
		}
		for _, issue := range result.Issues {
			if !issue.IsPullRequest() {
				continue
			}
			prs = append(prs, issue.GetNumber())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return prs, nil
}

// PostReview submits a review with inline comments + summary body.
func (s *Source) PostReview(ctx context.Context, ref ports.PrRef, review ports.ReviewPayload) (ports.PostedReview, error) {
	owner, name, err := parseRepoId(ref.RepoId)
	if err != nil {
		return ports.PostedReview{}, err
	}
	comments := make([]*github.DraftReviewComment, 0, len(review.Comments))
	for i := range review.Comments {
		c := &review.Comments[i]
		dc := &github.DraftReviewComment{
			Path: github.String(c.File),
			Body: github.String(c.Body),
			Side: github.String("RIGHT"),
			Line: github.Int(c.EndLine),
		}
		if c.StartLine > 0 && c.StartLine < c.EndLine {
			dc.StartLine = github.Int(c.StartLine)
			dc.StartSide = github.String("RIGHT")
		}
		comments = append(comments, dc)
	}
	event := "COMMENT"
	r, _, err := s.client.PullRequests.CreateReview(ctx, owner, name, ref.PrNumber, &github.PullRequestReviewRequest{
		CommitID: github.String(ref.HeadSha),
		Body:     github.String(review.Body),
		Event:    github.String(event),
		Comments: comments,
	})
	if err != nil {
		return ports.PostedReview{}, fmt.Errorf("create review: %w", err)
	}
	return ports.PostedReview{
		ReviewId:   r.GetID(),
		PostedAt:   r.GetSubmittedAt().Time,
		CommentIds: nil, // GitHub does not return inline comment ids from CreateReview; slice 2 can re-fetch
	}, nil
}

// UpdateCheck creates a completed check run for the head sha.
func (s *Source) UpdateCheck(ctx context.Context, ref ports.PrRef, result ports.CheckResult) error {
	owner, name, err := parseRepoId(ref.RepoId)
	if err != nil {
		return err
	}
	_, _, err = s.client.Checks.CreateCheckRun(ctx, owner, name, github.CreateCheckRunOptions{
		Name:       result.Name,
		HeadSHA:    ref.HeadSha,
		Status:     github.String("completed"),
		Conclusion: github.String(result.Conclusion),
		Output: &github.CheckRunOutput{
			Title:   github.String(result.Name),
			Summary: github.String(result.Summary),
		},
		DetailsURL: nonEmpty(result.DetailsURL),
	})
	if err != nil {
		return fmt.Errorf("create check: %w", err)
	}
	return nil
}

func parseRepoId(id ports.RepoId) (owner, name string, err error) {
	parts := strings.SplitN(string(id), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo_id %q (expected owner/name)", id)
	}
	return parts[0], parts[1], nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
