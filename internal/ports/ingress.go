package ports

import (
	"context"
	"net/http"
)

// HttpIngress is the HTTP front door, abstracted so the gateway can run
// behind ALB, Caddy, or stdlib net/http without code changes. The pilot
// adapter wraps chi.
type HttpIngress interface {
	Start(ctx context.Context, routes []RouteDef, opts StartOpts) (Server, error)
}

// RouteDef registers a handler for an HTTP method + path.
type RouteDef struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

// StartOpts configures the listener.
type StartOpts struct {
	Addr            string // e.g. ":8080"
	ReadTimeoutSec  int
	WriteTimeoutSec int
}

// Server is the running listener. Stop performs a graceful shutdown.
type Server interface {
	Stop(ctx context.Context) error
}
