package ports

import "fmt"

// VcsRegistry resolves a VcsSource by provider name (slice 6B). It
// replaces the singleton VcsSource that pipelines used in slices 0-6A;
// each ReviewJob / FeedbackJob carries a Provider that the pipeline
// uses to pick the right adapter.
//
// Single-VCS deployments register one adapter under one key; multi-VCS
// deployments register both. The registry's "github" entry is the
// implicit default for refs missing a Provider field (back-compat with
// data written before slice 6B).
type VcsRegistry interface {
	// For returns the VcsSource for provider, falling back to "github"
	// when provider is empty. Returns an error when the requested
	// provider isn't registered.
	For(provider VcsProvider) (VcsSource, error)
	// Providers returns the registered provider names, in stable order.
	// Useful for boot-time logging and webhook-gateway route registration.
	Providers() []VcsProvider
}

// MapVcsRegistry is the canonical in-process implementation. The boot
// package constructs one of these from the [vcs] / [vcs.github] /
// [vcs.bitbucket] config blocks.
type MapVcsRegistry struct {
	Sources map[VcsProvider]VcsSource
}

// For implements VcsRegistry.
func (m *MapVcsRegistry) For(provider VcsProvider) (VcsSource, error) {
	if provider == "" {
		provider = VcsProviderGitHub
	}
	s, ok := m.Sources[provider]
	if !ok {
		return nil, fmt.Errorf("no vcs adapter registered for provider %q", provider)
	}
	return s, nil
}

// Providers returns the registered provider names.
func (m *MapVcsRegistry) Providers() []VcsProvider {
	out := make([]VcsProvider, 0, len(m.Sources))
	// Stable order: github first when present, then alphabetical.
	if _, ok := m.Sources[VcsProviderGitHub]; ok {
		out = append(out, VcsProviderGitHub)
	}
	for k := range m.Sources {
		if k == VcsProviderGitHub {
			continue
		}
		out = append(out, k)
	}
	return out
}
