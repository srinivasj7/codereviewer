package admin

import "testing"

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		host    string
		allowed []string
		want    bool
	}{
		{"jira.acme.com", []string{"jira.acme.com"}, true},
		{"jira.acme.com:443", []string{"jira.acme.com"}, true},
		{"Jira.Acme.COM", []string{"jira.acme.com"}, true},
		{"evil.example", []string{"jira.acme.com"}, false},
		{"jira.acme.com", nil, false},
		{"jira.acme.com", []string{}, false},
		{"docs.google.com", []string{"jira.acme.com", "docs.google.com"}, true},
		{"docs.google.com ", []string{" docs.google.com "}, true},
	}
	for _, c := range cases {
		if got := hostAllowed(c.host, c.allowed); got != c.want {
			t.Errorf("hostAllowed(%q, %v) = %v, want %v", c.host, c.allowed, got, c.want)
		}
	}
}
