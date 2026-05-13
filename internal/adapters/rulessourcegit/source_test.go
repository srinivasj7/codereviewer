package rulessourcegit

import "testing"

func TestMatchesPattern_DoubleStar(t *testing.T) {
	cases := []struct {
		path, pattern string
		want          bool
	}{
		{"rules/sec/auth.md", "rules/**/*.md", true},
		{"rules/auth.md", "rules/**/*.md", true},
		{"rules/sec/deep/nested/auth.md", "rules/**/*.md", true},
		{"docs/auth.md", "rules/**/*.md", false},
		{"rules/auth.txt", "rules/**/*.md", false},
		{"README.md", "rules/**/*.md", false},
	}
	for _, c := range cases {
		got := matchesPattern(c.path, c.pattern)
		if got != c.want {
			t.Errorf("matchesPattern(%q, %q) = %v, want %v", c.path, c.pattern, got, c.want)
		}
	}
}

func TestMatchesPattern_Flat(t *testing.T) {
	// No `**` falls back to filepath.Match.
	if matchesPattern("rules/auth.md", "rules/auth.md") != true {
		t.Errorf("exact-match flat pattern failed")
	}
	if matchesPattern("rules/auth.md", "rules/*.md") != true {
		t.Errorf("single-dir flat pattern failed")
	}
	if matchesPattern("rules/sec/auth.md", "rules/*.md") != false {
		t.Errorf("flat pattern should not cross / boundaries")
	}
}
