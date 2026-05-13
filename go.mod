module codereviewer

go 1.25.7

// Force tree-sitter language packages to come from the main module
// (subdirectories), not from the separately-published sub-modules
// which conflict with the parent. Without these, mod tidy errors with
// "ambiguous import".
exclude github.com/smacker/go-tree-sitter/javascript v0.0.1

require (
	github.com/bradleyfalzon/ghinstallation/v2 v2.18.0
	github.com/go-chi/chi/v5 v5.2.5
	github.com/google/go-github/v66 v66.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/nats-io/nats.go v1.52.0
	github.com/pelletier/go-toml/v2 v2.2.3
	github.com/pgvector/pgvector-go v0.3.0
	github.com/pressly/goose/v3 v3.27.1
	github.com/sashabaranov/go-openai v1.41.2
	github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82
)

require (
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/google/go-github/v84 v84.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
