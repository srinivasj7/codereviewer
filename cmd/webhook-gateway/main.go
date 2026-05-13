// webhook-gateway terminates inbound VCS webhooks. It verifies the
// HMAC signature via the VcsSource adapter, routes events to the
// appropriate bus queue, and acks the delivery with 202 Accepted.
//
// Slice 1: hardcoded :8080. Slice 2 plumbs the listen address through
// config and adds slash-command parsing (/review, /improve, /ask).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

// defaultTenantId is the single-tenant placeholder for slice 1. A
// per-tenant routing table arrives with multi-tenant support.
const defaultTenantId = ports.TenantId("default-tenant")

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "webhook-gateway:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	obs := boot.PickObservability(cfg.Observability)
	secrets, err := boot.PickSecrets(cfg.Secrets)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	vcs, err := boot.PickVcs(cfg.Vcs, secrets)
	if err != nil {
		return fmt.Errorf("vcs: %w", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Get("/health", healthHandler(bus))
	r.Post("/github/webhook", webhookHandler(vcs, bus, obs))

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		obs.Logger.Info("webhook-gateway listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			obs.Logger.Error("listen failed", "err", err.Error())
		}
	}()

	<-ctx.Done()
	obs.Logger.Info("webhook-gateway shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func healthHandler(bus ports.MessageBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := bus.Health(r.Context())
		if err != nil || !status.Healthy {
			http.Error(w, "unhealthy: "+status.Detail, http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	}
}

func webhookHandler(vcs ports.VcsSource, bus ports.MessageBus, obs ports.Obs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		event, err := vcs.VerifyWebhook(r.Context(), r.Header, body)
		if err != nil {
			obs.Logger.Warn("webhook rejected",
				"err", err.Error(),
				"delivery", r.Header.Get("X-GitHub-Delivery"),
				"event", r.Header.Get("X-GitHub-Event"),
			)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := route(r.Context(), bus, event); err != nil {
			obs.Logger.Error("route webhook failed",
				"err", err.Error(),
				"kind", string(event.Kind),
				"delivery", event.DeliveryId,
			)
			http.Error(w, "route", http.StatusInternalServerError)
			return
		}
		obs.Logger.Info("webhook accepted",
			"kind", string(event.Kind),
			"delivery", event.DeliveryId,
		)
		w.WriteHeader(http.StatusAccepted)
	}
}

func route(ctx context.Context, bus ports.MessageBus, event ports.WebhookEvent) error {
	switch event.Kind {
	case ports.WebhookKindPullRequest:
		return routePullRequest(ctx, bus, event.PullRequest)
	case ports.WebhookKindPush:
		return routePush(ctx, bus, event.Push)
	case ports.WebhookKindReviewComment:
		// Slash commands (/review, /improve, /ask) arrive here. Slice 2
		// adds parsing and routing.
		return nil
	case ports.WebhookKindReaction:
		// Feedback signals; slice 4.
		return nil
	}
	return nil
}

func routePullRequest(ctx context.Context, bus ports.MessageBus, p *ports.PullRequestPayload) error {
	if p == nil {
		return nil
	}
	switch p.Action {
	case "opened", "synchronize", "reopened":
		// continue
	default:
		return nil
	}
	if p.IsDraft {
		return nil
	}
	ref := p.Ref
	ref.TenantId = defaultTenantId
	return schemas.PublishReviewJob(ctx, bus, schemas.ReviewJob{
		PrRef:   ref,
		Trigger: triggerFor(p.Action),
	})
}

func routePush(ctx context.Context, bus ports.MessageBus, p *ports.PushPayload) error {
	if p == nil {
		return nil
	}
	expected := "refs/heads/" + p.Repo.DefaultBranch
	if p.Ref != expected {
		return nil
	}
	return schemas.PublishIndexJob(ctx, bus, schemas.IndexJob{
		TenantId:  defaultTenantId,
		RepoId:    p.Repo.RepoId,
		Ref:       p.Ref,
		BeforeSha: p.BeforeSha,
		HeadSha:   p.HeadSha,
	})
}

func triggerFor(action string) ports.Trigger {
	if action == "synchronize" {
		return ports.TriggerPrUpdated
	}
	return ports.TriggerPrOpened
}
