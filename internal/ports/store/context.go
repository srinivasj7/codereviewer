package store

import (
	"context"
	"time"

	"codereviewer/internal/ports"
)

// InstructionSet is a named, reusable review-instruction block.
type InstructionSet struct {
	SetId     string
	TenantId  ports.TenantId
	Name      string
	Body      string
	UpdatedAt time.Time
	UpdatedBy string
}

// PrContextItem is one piece of ad-hoc context attached to a PR.
type PrContextItem struct {
	ItemId    string
	TenantId  ports.TenantId
	RepoId    ports.RepoId
	PrNumber  int
	Source    string // "text" | "file:<name>" | "url:<host>" | "command"
	Title     string
	Body      string
	CreatedAt time.Time
	CreatedBy string
}

// ContextStore handles instruction sets, their repo assignments, and
// ad-hoc per-PR context items.
type ContextStore interface {
	// Instruction sets.
	UpsertInstructionSet(ctx context.Context, s InstructionSet) error
	ListInstructionSets(ctx context.Context, tenant ports.TenantId) ([]InstructionSet, error)
	GetInstructionSet(ctx context.Context, setId string) (InstructionSet, bool, error)
	DeleteInstructionSet(ctx context.Context, setId string) error

	// Repo assignment.
	AssignSetToRepo(ctx context.Context, repoId ports.RepoId, setId string) error
	UnassignFromRepo(ctx context.Context, repoId ports.RepoId) error
	GetSetForRepo(ctx context.Context, repoId ports.RepoId) (InstructionSet, bool, error)

	// Per-PR ad-hoc items.
	AppendPrContext(ctx context.Context, item PrContextItem) error
	ListPrContext(ctx context.Context, ref ports.PrRef) ([]PrContextItem, error)
	DeletePrContextItem(ctx context.Context, itemId string) error
}
