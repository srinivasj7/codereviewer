package vcsbitbucket

import (
	"encoding/json"
	"fmt"
	"net/http"

	"codereviewer/internal/ports"
)

// parseEvent maps a Bitbucket Cloud webhook delivery into the canonical
// ports.WebhookEvent shape. The discriminator is the X-Event-Key header;
// X-Request-UUID is preserved as DeliveryId for audit linkage.
//
// Bitbucket has no top-level "reaction" event (reactions are nested
// inside comment payloads), and "pullrequest:approved" arrives as a
// review-approval event which we don't currently consume — both fall
// through to the unsupported error so the gateway can ack and ignore.
func parseEvent(headers http.Header, body []byte) (ports.WebhookEvent, error) {
	deliveryId := headers.Get("X-Request-UUID")
	event := headers.Get("X-Event-Key")
	switch event {
	case "pullrequest:created":
		return parsePullRequest(deliveryId, body, "opened")
	case "pullrequest:updated":
		return parsePullRequest(deliveryId, body, "synchronize")
	case "pullrequest:fulfilled", "pullrequest:rejected":
		return parsePullRequest(deliveryId, body, "closed")
	case "repo:push":
		return parsePush(deliveryId, body)
	case "pullrequest:comment_created", "pullrequest:comment_updated":
		return parseReviewComment(deliveryId, body)
	case "":
		return ports.WebhookEvent{}, fmt.Errorf("missing X-Event-Key header")
	}
	return ports.WebhookEvent{}, fmt.Errorf("unsupported webhook event %q", event)
}

type bbActor struct {
	DisplayName string `json:"display_name"`
	AccountId   string `json:"account_id"`
	Nickname    string `json:"nickname"`
	Type        string `json:"type"`
}

type bbRepo struct {
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	Workspace  struct {
		Slug string `json:"slug"`
	} `json:"workspace"`
	Mainbranch struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
}

type bbBranchSide struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		Hash string `json:"hash"`
	} `json:"commit"`
	Repository bbRepo `json:"repository"`
}

type bbPullRequest struct {
	Id          int          `json:"id"`
	Title       string       `json:"title"`
	State       string       `json:"state"`
	Source      bbBranchSide `json:"source"`
	Destination bbBranchSide `json:"destination"`
	Description string       `json:"description"`
}

type bbPrPayload struct {
	Repository  bbRepo        `json:"repository"`
	PullRequest bbPullRequest `json:"pullrequest"`
	Actor       bbActor       `json:"actor"`
}

func parsePullRequest(deliveryId string, body []byte, action string) (ports.WebhookEvent, error) {
	var p bbPrPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse pullrequest: %w", err)
	}
	if p.PullRequest.Id == 0 {
		return ports.WebhookEvent{}, fmt.Errorf("pullrequest payload missing id")
	}
	owner, name := splitFullName(p.Repository.FullName)
	repoId := ports.RepoId(p.Repository.FullName)
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindPullRequest,
		DeliveryId: deliveryId,
		PullRequest: &ports.PullRequestPayload{
			Action: action,
			Ref: ports.PrRef{
				RepoId:   repoId,
				PrNumber: p.PullRequest.Id,
				HeadSha:  p.PullRequest.Source.Commit.Hash,
			},
			Repo: ports.RepoRef{
				RepoId:        repoId,
				Owner:         owner,
				Name:          name,
				DefaultBranch: defaultBranchOr(p.Repository.Mainbranch.Name, "main"),
			},
			BaseSha: p.PullRequest.Destination.Commit.Hash,
		},
	}, nil
}

type bbPushChange struct {
	New struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Target struct {
			Hash string `json:"hash"`
		} `json:"target"`
	} `json:"new"`
	Old struct {
		Target struct {
			Hash string `json:"hash"`
		} `json:"target"`
	} `json:"old"`
}

type bbPushPayload struct {
	Repository bbRepo `json:"repository"`
	Push       struct {
		Changes []bbPushChange `json:"changes"`
	} `json:"push"`
}

func parsePush(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p bbPushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse push: %w", err)
	}
	defaultBranch := defaultBranchOr(p.Repository.Mainbranch.Name, "main")
	owner, name := splitFullName(p.Repository.FullName)
	// Indexer pipelines consume one (repo, headSha) per push event;
	// surface the first default-branch change. Other branches are
	// uninteresting for indexing today.
	for _, c := range p.Push.Changes {
		if c.New.Type != "branch" || c.New.Name != defaultBranch {
			continue
		}
		return ports.WebhookEvent{
			Kind:       ports.WebhookKindPush,
			DeliveryId: deliveryId,
			Push: &ports.PushPayload{
				Repo: ports.RepoRef{
					RepoId:        ports.RepoId(p.Repository.FullName),
					Owner:         owner,
					Name:          name,
					DefaultBranch: defaultBranch,
				},
				Ref:       "refs/heads/" + c.New.Name,
				BeforeSha: c.Old.Target.Hash,
				HeadSha:   c.New.Target.Hash,
			},
		}, nil
	}
	return ports.WebhookEvent{}, fmt.Errorf("push event had no default-branch change")
}

type bbCommentInline struct {
	From *int   `json:"from"`
	To   *int   `json:"to"`
	Path string `json:"path"`
}

type bbCommentParent struct {
	Id int64 `json:"id"`
}

type bbComment struct {
	Id      int64 `json:"id"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Inline *bbCommentInline `json:"inline"`
	User   bbActor          `json:"user"`
	Parent *bbCommentParent `json:"parent"`
}

type bbCommentPayload struct {
	Repository  bbRepo        `json:"repository"`
	PullRequest bbPullRequest `json:"pullrequest"`
	Comment     bbComment     `json:"comment"`
	Actor       bbActor       `json:"actor"`
}

func parseReviewComment(deliveryId string, body []byte) (ports.WebhookEvent, error) {
	var p bbCommentPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("parse comment: %w", err)
	}
	if p.PullRequest.Id == 0 || p.Comment.Id == 0 {
		return ports.WebhookEvent{}, fmt.Errorf("comment payload missing pr id or comment id")
	}
	var inReplyTo int64
	if p.Comment.Parent != nil {
		inReplyTo = p.Comment.Parent.Id
	}
	return ports.WebhookEvent{
		Kind:       ports.WebhookKindReviewComment,
		DeliveryId: deliveryId,
		ReviewComment: &ports.ReviewCommentPayload{
			Ref: ports.PrRef{
				RepoId:   ports.RepoId(p.Repository.FullName),
				PrNumber: p.PullRequest.Id,
				HeadSha:  p.PullRequest.Source.Commit.Hash,
			},
			CommentId:   p.Comment.Id,
			AuthorId:    nicknameOr(p.Comment.User),
			Body:        p.Comment.Content.Raw,
			IsBot:       p.Comment.User.Type == "team",
			InReplyToId: inReplyTo,
		},
	}, nil
}

func splitFullName(fullName string) (owner, name string) {
	for i := 0; i < len(fullName); i++ {
		if fullName[i] == '/' {
			return fullName[:i], fullName[i+1:]
		}
	}
	return fullName, ""
}

func defaultBranchOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nicknameOr(a bbActor) string {
	if a.Nickname != "" {
		return a.Nickname
	}
	if a.AccountId != "" {
		return a.AccountId
	}
	return a.DisplayName
}
