package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"codereviewer/internal/ports"
)

// TestWebhookHandler_UnconfiguredProvider asserts that a webhook POST
// for a provider that isn't registered with the gateway's VcsRegistry
// returns 404 without invoking any other dependency. This is the
// "single-VCS deploy receives a stray Bitbucket webhook" path.
func TestWebhookHandler_UnconfiguredProvider(t *testing.T) {
	g := &gateway{
		vcs: &ports.MapVcsRegistry{
			// GitHub-only deployment.
			Sources: map[ports.VcsProvider]ports.VcsSource{},
		},
		obs:      ports.Obs{Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))},
		tenantId: "tenant-a",
	}

	req := httptest.NewRequest(http.MethodPost, "/bitbucket/webhook",
		bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()

	handler := g.webhookHandler(ports.VcsProviderBitbucket)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unconfigured provider, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}
