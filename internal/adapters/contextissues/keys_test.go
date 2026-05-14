package contextissues

import (
	"reflect"
	"testing"
)

func TestJiraStyleKeys(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"PROJ-123: fix the thing"}, []string{"PROJ-123"}},
		{[]string{"ENG-1 and ENG-2", "ENG-1 again"}, []string{"ENG-1", "ENG-2"}},
		{[]string{"co-op stuff", "lowercase-1 stays out"}, nil},
		{[]string{"AB-1 CD-22 EF-333"}, []string{"AB-1", "CD-22", "EF-333"}},
		{[]string{"x"}, nil},
	}
	for _, c := range cases {
		got := JiraStyleKeys(c.in...)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("JiraStyleKeys(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestGithubIssueRefs(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"fixes #42"}, []string{"#42"}},
		{[]string{"see octo/repo#7 and #8"}, []string{"octo/repo#7", "#8"}},
		{[]string{"#1 #1 #2"}, []string{"#1", "#2"}},
	}
	for _, c := range cases {
		got := GithubIssueRefs(c.in...)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("GithubIssueRefs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitGithubRef(t *testing.T) {
	cases := []struct {
		in            string
		owner, repo   string
		num           int
		ok            bool
	}{
		{"#42", "", "", 42, true},
		{"octo/widgets#7", "octo", "widgets", 7, true},
		{"#0", "", "", 0, false},
		{"nothing here", "", "", 0, false},
		{"PROJ-1", "", "", 0, false},
		{"owner/#7", "", "", 0, false}, // empty repo half
		{"owner#7", "", "", 0, false},  // no slash
	}
	for _, c := range cases {
		owner, repo, num, ok := SplitGithubRef(c.in)
		if owner != c.owner || repo != c.repo || num != c.num || ok != c.ok {
			t.Errorf("SplitGithubRef(%q) = (%q,%q,%d,%v), want (%q,%q,%d,%v)",
				c.in, owner, repo, num, ok, c.owner, c.repo, c.num, c.ok)
		}
	}
}
