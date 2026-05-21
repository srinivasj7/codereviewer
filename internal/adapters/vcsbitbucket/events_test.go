package vcsbitbucket

import (
	"net/http"
	"strings"
	"testing"

	"codereviewer/internal/ports"
)

const samplePrCreated = `{
  "actor": {"nickname": "alice", "display_name": "Alice"},
  "repository": {
    "name": "demo",
    "full_name": "acme/demo",
    "mainbranch": {"name": "main"}
  },
  "pullrequest": {
    "id": 42,
    "title": "Add feature",
    "description": "PROJ-1: do the thing",
    "state": "OPEN",
    "source": {
      "branch": {"name": "feat/x"},
      "commit": {"hash": "deadbeefcafe"}
    },
    "destination": {
      "branch": {"name": "main"},
      "commit": {"hash": "baseba5e0000"}
    }
  }
}`

func TestParseEvent_PullRequestCreated(t *testing.T) {
	h := http.Header{
		"X-Event-Key":     {"pullrequest:created"},
		"X-Request-Uuid":  {"abc123"},
	}
	ev, err := parseEvent(h, []byte(samplePrCreated))
	if err != nil {
		t.Fatalf("parseEvent error: %v", err)
	}
	if ev.Kind != ports.WebhookKindPullRequest {
		t.Fatalf("kind: got %q", ev.Kind)
	}
	if ev.PullRequest == nil {
		t.Fatalf("PullRequest payload is nil")
	}
	if ev.PullRequest.Action != "opened" {
		t.Errorf("action: got %q want opened", ev.PullRequest.Action)
	}
	if got := string(ev.PullRequest.Ref.RepoId); got != "acme/demo" {
		t.Errorf("repoId: got %q", got)
	}
	if ev.PullRequest.Ref.PrNumber != 42 {
		t.Errorf("pr number: got %d", ev.PullRequest.Ref.PrNumber)
	}
	if ev.PullRequest.Ref.HeadSha != "deadbeefcafe" {
		t.Errorf("head sha: got %q", ev.PullRequest.Ref.HeadSha)
	}
	if ev.PullRequest.BaseSha != "baseba5e0000" {
		t.Errorf("base sha: got %q", ev.PullRequest.BaseSha)
	}
	if ev.PullRequest.Repo.Owner != "acme" || ev.PullRequest.Repo.Name != "demo" {
		t.Errorf("owner/name: got %q/%q", ev.PullRequest.Repo.Owner, ev.PullRequest.Repo.Name)
	}
	if ev.PullRequest.Repo.DefaultBranch != "main" {
		t.Errorf("default branch: got %q", ev.PullRequest.Repo.DefaultBranch)
	}
}

func TestParseEvent_PullRequestUpdatedMapsToSynchronize(t *testing.T) {
	h := http.Header{
		"X-Event-Key": {"pullrequest:updated"},
	}
	ev, err := parseEvent(h, []byte(samplePrCreated))
	if err != nil {
		t.Fatalf("parseEvent error: %v", err)
	}
	if ev.PullRequest.Action != "synchronize" {
		t.Errorf("action: got %q want synchronize", ev.PullRequest.Action)
	}
}

const samplePush = `{
  "repository": {
    "name": "demo",
    "full_name": "acme/demo",
    "mainbranch": {"name": "main"}
  },
  "push": {
    "changes": [
      {
        "new": {"type": "branch", "name": "feat/x", "target": {"hash": "ignoreThis"}},
        "old": {"target": {"hash": ""}}
      },
      {
        "new": {"type": "branch", "name": "main", "target": {"hash": "newSha111"}},
        "old": {"target": {"hash": "oldSha000"}}
      }
    ]
  }
}`

func TestParseEvent_PushPicksDefaultBranchChange(t *testing.T) {
	h := http.Header{"X-Event-Key": {"repo:push"}}
	ev, err := parseEvent(h, []byte(samplePush))
	if err != nil {
		t.Fatalf("parseEvent error: %v", err)
	}
	if ev.Kind != ports.WebhookKindPush {
		t.Fatalf("kind: got %q", ev.Kind)
	}
	if ev.Push.HeadSha != "newSha111" {
		t.Errorf("head sha: got %q", ev.Push.HeadSha)
	}
	if ev.Push.BeforeSha != "oldSha000" {
		t.Errorf("before sha: got %q", ev.Push.BeforeSha)
	}
	if ev.Push.Ref != "refs/heads/main" {
		t.Errorf("ref: got %q", ev.Push.Ref)
	}
}

const sampleCommentReply = `{
  "actor": {"nickname": "bob"},
  "repository": {"full_name": "acme/demo", "mainbranch": {"name": "main"}},
  "pullrequest": {
    "id": 7,
    "source": {"commit": {"hash": "abc123"}}
  },
  "comment": {
    "id": 999,
    "content": {"raw": "thanks!"},
    "user": {"nickname": "bob"},
    "parent": {"id": 888}
  }
}`

func TestParseEvent_CommentReplyCarriesInReplyTo(t *testing.T) {
	h := http.Header{"X-Event-Key": {"pullrequest:comment_created"}}
	ev, err := parseEvent(h, []byte(sampleCommentReply))
	if err != nil {
		t.Fatalf("parseEvent error: %v", err)
	}
	if ev.Kind != ports.WebhookKindReviewComment {
		t.Fatalf("kind: got %q", ev.Kind)
	}
	if ev.ReviewComment.CommentId != 999 {
		t.Errorf("comment id: got %d", ev.ReviewComment.CommentId)
	}
	if ev.ReviewComment.InReplyToId != 888 {
		t.Errorf("in reply to: got %d", ev.ReviewComment.InReplyToId)
	}
	if ev.ReviewComment.Body != "thanks!" {
		t.Errorf("body: got %q", ev.ReviewComment.Body)
	}
}

func TestParseEvent_UnknownEventErrors(t *testing.T) {
	h := http.Header{"X-Event-Key": {"pullrequest:approved"}}
	_, err := parseEvent(h, []byte(`{}`))
	if err == nil {
		t.Fatalf("expected error for unsupported event")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error doesn't mention unsupported: %v", err)
	}
}

func TestSplitFullName(t *testing.T) {
	o, n := splitFullName("acme/demo-repo")
	if o != "acme" || n != "demo-repo" {
		t.Errorf("split: got %q/%q", o, n)
	}
	o, n = splitFullName("nodash")
	if o != "nodash" || n != "" {
		t.Errorf("no-slash: got %q/%q", o, n)
	}
}
