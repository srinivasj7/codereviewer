package ports

import "context"

// SecretsProvider resolves named secrets at runtime. Pilot adapters:
// secretsenv (process env) and (slice 1+) secretsaws (AWS Secrets Manager).
// Implementations MUST NOT log resolved values.
type SecretsProvider interface {
	Get(ctx context.Context, name string) (string, error)
}
