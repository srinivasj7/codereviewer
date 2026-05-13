// Package secretsenv resolves secrets from process environment variables.
// Suitable for local dev and minimal EC2 deployments where systemd or
// docker provides env-var injection. Production cloud profiles should
// use secretsaws or secretsvault.
package secretsenv

import (
	"context"
	"fmt"
	"os"

	"codereviewer/internal/ports"
)

// Provider implements ports.SecretsProvider over os.Getenv.
type Provider struct{}

// New returns a Provider.
func New() ports.SecretsProvider { return Provider{} }

// Get returns the env var named name. Empty values are treated as missing
// to avoid silently passing blank credentials downstream.
func (Provider) Get(_ context.Context, name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("secretsenv: %q not set", name)
	}
	return v, nil
}
