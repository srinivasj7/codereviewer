package schemas

// GitHub webhook payload subsets. These are intentionally minimal —
// only the fields the system reads. Full schemas live in go-github.
// Real parsing/validation moves into internal/adapters/vcsgithub
// in slice 1; for slice 0 these types document the contract.

// GithubRepoRef is the embedded repo block on most webhook events.
type GithubRepoRef struct {
	Name  string `json:"name"`
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
	DefaultBranch string `json:"default_branch"`
}

// GithubPullRequestEvent is fired on pull_request.
type GithubPullRequestEvent struct {
	Action      string `json:"action"` // opened | synchronize | closed | edited | ...
	PullRequest struct {
		Number int  `json:"number"`
		Draft  bool `json:"draft"`
		Head   struct {
			Sha string `json:"sha"`
		} `json:"head"`
		Base struct {
			Sha string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository GithubRepoRef `json:"repository"`
}

// GithubPushEvent is fired on push.
type GithubPushEvent struct {
	Ref        string        `json:"ref"`
	Before     string        `json:"before"`
	After      string        `json:"after"`
	Repository GithubRepoRef `json:"repository"`
}

// GithubReviewCommentEvent is fired on pull_request_review_comment.
type GithubReviewCommentEvent struct {
	Action  string `json:"action"`
	Comment struct {
		Id          int64  `json:"id"`
		InReplyToId int64  `json:"in_reply_to_id"`
		Body        string `json:"body"`
		Path        string `json:"path"`
		User        struct {
			Login string `json:"login"`
			Type  string `json:"type"` // "Bot" | "User"
		} `json:"user"`
	} `json:"comment"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			Sha string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository GithubRepoRef `json:"repository"`
}

// GithubReactionEvent is fired on reaction.
type GithubReactionEvent struct {
	Action   string `json:"action"`
	Reaction struct {
		Content string `json:"content"` // "+1" | "-1" | ...
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"reaction"`
	Comment struct {
		Id int64 `json:"id"`
	} `json:"comment"`
}
