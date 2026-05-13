// Package rulessourcegit fetches rule files from a git repository.
// It uses the `git` CLI via os/exec rather than a Go-native git library
// for two reasons: the canonical implementation is the most-audited
// option, and we avoid pulling a large dependency graph for what is
// fundamentally a clone+walk operation.
//
// Requirement: a git binary on PATH. The rules-sync Docker image
// installs git via `apk add git` in its build stage.
package rulessourcegit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"codereviewer/internal/ports"
)

// Source clones the rules repo to a temp dir and reads matching files.
type Source struct {
	pattern string // glob: support `dir/**/*.ext`
}

// New returns a Source that picks up files matching pattern. Empty
// pattern defaults to `rules/**/*.md`.
func New(pattern string) *Source {
	if pattern == "" {
		pattern = "rules/**/*.md"
	}
	return &Source{pattern: pattern}
}

// FetchAt clones gitUrl at ref (typically the default branch name)
// into a temp directory, lists matching files, and returns their raw
// contents along with the resolved commit sha. The temp dir is
// cleaned up before this returns.
func (s *Source) FetchAt(ctx context.Context, gitUrl, ref string) (ports.RulesSnapshot, error) {
	if gitUrl == "" {
		return ports.RulesSnapshot{}, fmt.Errorf("rulessourcegit: git_url is empty")
	}
	if ref == "" {
		ref = "main"
	}
	tmpDir, err := os.MkdirTemp("", "rules-*")
	if err != nil {
		return ports.RulesSnapshot{}, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cloneCmd := exec.CommandContext(ctx, "git", "clone",
		"--depth=1", "--branch", ref, "--single-branch",
		gitUrl, tmpDir,
	)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return ports.RulesSnapshot{}, fmt.Errorf("git clone %s@%s: %w (%s)", gitUrl, ref, err, strings.TrimSpace(string(out)))
	}

	shaCmd := exec.CommandContext(ctx, "git", "-C", tmpDir, "rev-parse", "HEAD")
	shaBytes, err := shaCmd.Output()
	if err != nil {
		return ports.RulesSnapshot{}, fmt.Errorf("git rev-parse: %w", err)
	}
	commitSha := strings.TrimSpace(string(shaBytes))

	var files []ports.RawRuleFile
	walkErr := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), ".git") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(tmpDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !matchesPattern(rel, s.pattern) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		files = append(files, ports.RawRuleFile{Path: rel, Content: content})
		return nil
	})
	if walkErr != nil {
		return ports.RulesSnapshot{}, fmt.Errorf("walk: %w", walkErr)
	}
	return ports.RulesSnapshot{CommitSha: commitSha, Files: files}, nil
}

// matchesPattern supports the limited glob shape `prefix/**/*.ext`.
// path.Match (slash-separated, cross-platform consistent) doesn't
// natively understand `**`, so we split the pattern on `**` and match
// prefix + suffix-on-basename manually. Uses path.Match rather than
// filepath.Match so the separator semantics are uniform on Windows
// (where filepath.Match treats `\` as the separator and `/` as content).
func matchesPattern(p, pattern string) bool {
	if !strings.Contains(pattern, "**") {
		matched, _ := path.Match(pattern, p)
		return matched
	}
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")
	if prefix != "" && !strings.HasPrefix(p, prefix+"/") {
		return false
	}
	if suffix == "" {
		return true
	}
	matched, _ := path.Match(suffix, filepath.Base(p))
	return matched
}
