package contextrepoinstructions_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/contextrepoinstructions"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/testing/fakes"
)

const (
	tenant = ports.TenantId("t")
	repo   = ports.RepoId("octo/widgets")
)

func ref() ports.PrRef {
	return ports.PrRef{TenantId: tenant, RepoId: repo, PrNumber: 1, HeadSha: "head"}
}

func TestFileOverridesAssignedSet(t *testing.T) {
	vcs := fakes.NewVcs()
	vcs.SetFileAt(repo, "head", ".codereviewer.md", "use the query builder; never raw SQL")

	ctxStore := fakes.NewContextStore()
	require.NoError(t, ctxStore.UpsertInstructionSet(context.Background(), store.InstructionSet{
		SetId: "set-1", TenantId: tenant, Name: "Go services", Body: "go conventions",
	}))
	require.NoError(t, ctxStore.AssignSetToRepo(context.Background(), repo, "set-1"))

	p := contextrepoinstructions.New(vcs, ctxStore, obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Contains(t, items[0].Body, "use the query builder")
	require.NotContains(t, items[0].Body, "go conventions", "file must override DB set")
}

func TestAssignedSetUsedWhenNoFile(t *testing.T) {
	vcs := fakes.NewVcs() // no SetFileAt -> returns error -> provider treats as absent
	ctxStore := fakes.NewContextStore()
	require.NoError(t, ctxStore.UpsertInstructionSet(context.Background(), store.InstructionSet{
		SetId: "set-1", TenantId: tenant, Name: "Go", Body: "no panics in handlers",
	}))
	require.NoError(t, ctxStore.AssignSetToRepo(context.Background(), repo, "set-1"))

	p := contextrepoinstructions.New(vcs, ctxStore, obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Contains(t, items[0].Body, "no panics")
}

func TestNoInstructionsReturnsEmpty(t *testing.T) {
	vcs := fakes.NewVcs()
	p := contextrepoinstructions.New(vcs, fakes.NewContextStore(), obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref())
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestEmptyFileFallsThrough(t *testing.T) {
	vcs := fakes.NewVcs()
	vcs.SetFileAt(repo, "head", ".codereviewer.md", "   \n\n   ")

	ctxStore := fakes.NewContextStore()
	require.NoError(t, ctxStore.UpsertInstructionSet(context.Background(), store.InstructionSet{
		SetId: "set-1", TenantId: tenant, Name: "Go", Body: "fallback body",
	}))
	require.NoError(t, ctxStore.AssignSetToRepo(context.Background(), repo, "set-1"))

	p := contextrepoinstructions.New(vcs, ctxStore, obsstdout.New("test"))
	items, err := p.Fetch(context.Background(), ref())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Contains(t, items[0].Body, "fallback body")
}
