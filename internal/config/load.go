// Package config loads the TOML config file and expands ${ENV_VAR}
// references from the process environment. Validation lives in
// internal/schemas/config.go.
package config

import (
	"fmt"
	"os"
	"regexp"

	"github.com/pelletier/go-toml/v2"

	"codereviewer/internal/schemas"
)

// Load reads, env-expands, parses, and validates a TOML config file.
func Load(path string) (*schemas.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	expanded := ExpandEnv(string(data))
	var cfg schemas.Config
	if err := toml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv replaces ${NAME} occurrences with the value of the matching
// environment variable. Missing variables expand to the empty string;
// the validator catches structurally required fields afterwards.
func ExpandEnv(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		return os.Getenv(name)
	})
}
