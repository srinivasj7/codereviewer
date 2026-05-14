// Package contextissues holds the issue-key extraction logic shared by
// all issue-tracker context providers. Each provider has its own
// authentication path and fetch shape, but they all start from "find
// the issue keys mentioned somewhere on this PR."
//
// The PR title, branch name, and body are searched. Each search yields
// a set; the union (deduped, order-preserving) is returned.
package contextissues

import (
	"regexp"
	"strings"
)

// jiraStyle matches PROJ-123 or ABC-1. Uppercase letters + dash + digits.
// Linear keys follow the same shape; the caller decides which tracker
// to ask. Lowercase prefixes are intentionally excluded so common words
// like "co-op" don't match.
var jiraStyle = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)

// githubIssue matches #123 or owner/repo#123. The cross-repo form is
// returned as-is; #123 is returned in its short form for the caller to
// scope to the current repo.
var githubIssue = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?#\d+`)

// JiraStyleKeys returns the deduped set of PROJ-123-style keys found
// across the provided strings. Order of first appearance is preserved.
func JiraStyleKeys(parts ...string) []string {
	return uniqueMatches(jiraStyle, parts)
}

// GithubIssueRefs returns #123 / owner/repo#123 references in order
// of first appearance.
func GithubIssueRefs(parts ...string) []string {
	return uniqueMatches(githubIssue, parts)
}

func uniqueMatches(re *regexp.Regexp, parts []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		for _, m := range re.FindAllString(p, -1) {
			m = strings.TrimSpace(m)
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}

// SplitGithubRef parses "#123" or "owner/repo#123" into (owner, repo,
// number, ok). Empty owner/repo signal the caller should use the
// current PR's repo.
func SplitGithubRef(ref string) (owner, repo string, number int, ok bool) {
	hash := strings.IndexByte(ref, '#')
	if hash < 0 {
		return "", "", 0, false
	}
	left := ref[:hash]
	right := ref[hash+1:]
	if left != "" {
		slash := strings.IndexByte(left, '/')
		if slash < 0 {
			return "", "", 0, false
		}
		owner = left[:slash]
		repo = left[slash+1:]
		if owner == "" || repo == "" {
			return "", "", 0, false
		}
	}
	if right == "" {
		return "", "", 0, false
	}
	n := 0
	for _, r := range right {
		if r < '0' || r > '9' {
			return "", "", 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return "", "", 0, false
	}
	return owner, repo, n, true
}
