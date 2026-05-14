package contextadhoc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/contextadhoc"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/testing/fakes"
)

func TestNewestFirst_CappedAtMaxN(t *testing.T) {
	cs := fakes.NewContextStore()
	ref := ports.PrRef{TenantId: "t", RepoId: "r", PrNumber: 1}
	for i := 0; i < 5; i++ {
		require.NoError(t, cs.AppendPrContext(context.Background(), store.PrContextItem{
			TenantId: ref.TenantId, RepoId: ref.RepoId, PrNumber: ref.PrNumber,
			Source: "text", Body: "item",
		}))
	}

	p := contextadhoc.New(cs, 2, obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 2, "must cap at maxN")
	require.Equal(t, "ad-hoc", items[0].Source)
}

func TestEmptyBodiesFilteredOut(t *testing.T) {
	cs := fakes.NewContextStore()
	ref := ports.PrRef{TenantId: "t", RepoId: "r", PrNumber: 1}
	require.NoError(t, cs.AppendPrContext(context.Background(), store.PrContextItem{
		TenantId: ref.TenantId, RepoId: ref.RepoId, PrNumber: ref.PrNumber,
		Source: "text", Body: "  \n\n",
	}))
	require.NoError(t, cs.AppendPrContext(context.Background(), store.PrContextItem{
		TenantId: ref.TenantId, RepoId: ref.RepoId, PrNumber: ref.PrNumber,
		Source: "text", Body: "real body",
	}))

	p := contextadhoc.New(cs, 10, obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "real body", items[0].Body)
}
