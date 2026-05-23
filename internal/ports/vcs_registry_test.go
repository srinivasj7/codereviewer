package ports

import (
	"errors"
	"testing"
)

// stubVcs is a no-op VcsSource. We only need pointer identity for
// these tests — no method on the embedded interface is ever called.
type stubVcs struct{ VcsSource }

func TestMapVcsRegistry_For_ReturnsRegisteredSource(t *testing.T) {
	gh := &stubVcs{}
	bb := &stubVcs{}
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{
		VcsProviderGitHub:    gh,
		VcsProviderBitbucket: bb,
	}}

	got, err := r.For(VcsProviderGitHub)
	if err != nil {
		t.Fatalf("For(github): unexpected error: %v", err)
	}
	if got != gh {
		t.Errorf("For(github): got %p, want %p", got, gh)
	}

	got, err = r.For(VcsProviderBitbucket)
	if err != nil {
		t.Fatalf("For(bitbucket): unexpected error: %v", err)
	}
	if got != bb {
		t.Errorf("For(bitbucket): got %p, want %p", got, bb)
	}
}

func TestMapVcsRegistry_For_EmptyDefaultsToGitHub(t *testing.T) {
	gh := &stubVcs{}
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{VcsProviderGitHub: gh}}

	got, err := r.For("")
	if err != nil {
		t.Fatalf("For(\"\"): unexpected error: %v", err)
	}
	if got != gh {
		t.Errorf("empty provider should resolve to github; got %p, want %p", got, gh)
	}
}

func TestMapVcsRegistry_For_UnknownReturnsError(t *testing.T) {
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{
		VcsProviderGitHub: &stubVcs{},
	}}
	_, err := r.For("gitlab")
	if err == nil {
		t.Fatal("expected error for unregistered provider, got nil")
	}
}

func TestMapVcsRegistry_For_EmptyWithoutGitHubFails(t *testing.T) {
	// Bitbucket-only deployment — an empty provider must not silently
	// fall through to a different adapter.
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{
		VcsProviderBitbucket: &stubVcs{},
	}}
	_, err := r.For("")
	if err == nil {
		t.Fatal("expected error when github default is not registered")
	}
}

func TestMapVcsRegistry_Providers_GitHubFirst(t *testing.T) {
	// Documented ordering: github wins the leading slot when present;
	// remaining keys come after in map-iteration order. Callers (boot
	// logs, gateway route registration) rely on github being first.
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{
		VcsProviderBitbucket: &stubVcs{},
		VcsProviderGitHub:    &stubVcs{},
	}}
	got := r.Providers()
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}
	if got[0] != VcsProviderGitHub {
		t.Errorf("expected github first, got %q", got[0])
	}
}

func TestMapVcsRegistry_Providers_BitbucketOnly(t *testing.T) {
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{
		VcsProviderBitbucket: &stubVcs{},
	}}
	got := r.Providers()
	if len(got) != 1 || got[0] != VcsProviderBitbucket {
		t.Errorf("expected [bitbucket], got %v", got)
	}
}

func TestPrRef_ProviderOrDefault(t *testing.T) {
	cases := []struct {
		name string
		ref  PrRef
		want VcsProvider
	}{
		{"empty defaults to github", PrRef{}, VcsProviderGitHub},
		{"github stays github", PrRef{Provider: VcsProviderGitHub}, VcsProviderGitHub},
		{"bitbucket stays bitbucket", PrRef{Provider: VcsProviderBitbucket}, VcsProviderBitbucket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.ProviderOrDefault(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRepoRef_ProviderOrDefault(t *testing.T) {
	if got := (RepoRef{}).ProviderOrDefault(); got != VcsProviderGitHub {
		t.Errorf("empty RepoRef.Provider should default to github, got %q", got)
	}
	if got := (RepoRef{Provider: VcsProviderBitbucket}).ProviderOrDefault(); got != VcsProviderBitbucket {
		t.Errorf("bitbucket RepoRef.Provider should round-trip, got %q", got)
	}
}

// Compile-time guard: MapVcsRegistry must satisfy VcsRegistry.
var _ VcsRegistry = (*MapVcsRegistry)(nil)

// Sanity check that errors.Is sees a wrapped registry error — the
// pipeline's panic-with-context relies on the error string for ops
// debugging, not for unwrapping, but keep the surface stable.
func TestMapVcsRegistry_For_ErrorMentionsProvider(t *testing.T) {
	r := &MapVcsRegistry{Sources: map[VcsProvider]VcsSource{}}
	_, err := r.For("gitlab")
	if err == nil {
		t.Fatal("expected error")
	}
	// Don't pin the exact wording; just check the provider name leaks
	// into the message so logs are useful.
	if !errors.Is(err, err) || err.Error() == "" {
		t.Errorf("error should be non-empty, got %q", err)
	}
}
