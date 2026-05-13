// webhook-gateway terminates inbound VCS webhooks. It verifies the
// HMAC signature via the VcsSource adapter, auto-registers the source
// repo (and its tenant) in the database the first time it's seen, and
// routes the event to the appropriate bus queue.
//
// Slash commands in PR comments:
//   - "/review" re-triggers a review job against the current head
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

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

	secrets, err := boot.PickSecrets(cfg.Secrets)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	vcs, err := boot.PickVcs(cfg.Vcs, secrets)
	if err != nil {
		return fmt.Errorf("vcs: %w", err)
	}

	stores, err := boot.PickStores(ctx, cfg.Store, obs)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if stores.Close != nil {
		defer stores.Close()
	}

	gw := &gateway{
		vcs:      vcs,
		bus:      bus,
		repos:    stores.Repos,
		obs:      obs,
		tenantId: ports.TenantId(cfg.Tenant.Id),
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Get("/health", gw.health)
	r.Post("/github/webhook", gw.webhook)

	srv := &http.Server{
		Addr:              cfg.Gateway.ListenAddr,
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

// flushObs gives the OTel exporters a small window to drain. Errors are
// dropped — at shutdown time there's no actionable handler.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

type gateway struct {
	vcs      ports.VcsSource
	bus      ports.MessageBus
	repos    store.RepoStore
	obs      ports.Obs
	tenantId ports.TenantId
}

func (g *gateway) health(w http.ResponseWriter, r *http.Request) {
	status, err := g.bus.Health(r.Context())
	if err != nil || !status.Healthy {
		http.Error(w, "unhealthy: "+status.Detail, http.StatusServiceUnavailable)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

func (g *gateway) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	event, err := g.vcs.VerifyWebhook(r.Context(), r.Header, body)
	if err != nil {
		g.obs.Logger.Warn("webhook rejected",
			"err", err.Error(),
			"delivery", r.Header.Get("X-GitHub-Delivery"),
			"event", r.Header.Get("X-GitHub-Event"),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := g.route(r.Context(), event); err != nil {
		g.obs.Logger.Error("route webhook failed",
			"err", err.Error(),
			"kind", string(event.Kind),
			"delivery", event.DeliveryId,
		)
		http.Error(w, "route", http.StatusInternalServerError)
		return
	}
	g.obs.Logger.Info("webhook accepted",
		"kind", string(event.Kind),
		"delivery", event.DeliveryId,
	)
	w.WriteHeader(http.StatusAccepted)
}

func (g *gateway) route(ctx context.Context, event ports.WebhookEvent) error {
	switch event.Kind {
	case ports.WebhookKindPullRequest:
		return g.routePullRequest(ctx, event.PullRequest)
	case ports.WebhookKindPush:
		return g.routePush(ctx, event.Push)
	case ports.WebhookKindReviewComment:
		return g.routeReviewComment(ctx, event.ReviewComment)
	case ports.WebhookKindReaction:
		return g.routeReaction(ctx, event.Reaction)
	}
	return nil
}

func (g *gateway) routePullRequest(ctx context.Context, p *ports.PullRequestPayload) error {
	if p == nil {
		return nil
	}
	switch p.Action {
	case "opened", "synchronize", "reopened":
	default:
		return nil
	}
	if p.IsDraft {
		return nil
	}

	repo := p.Repo
	repo.TenantId = g.tenantId
	if err := g.ensureRepo(ctx, repo); err != nil {
		return err
	}

	ref := p.Ref
	ref.TenantId = g.tenantId
	return schemas.PublishReviewJob(ctx, g.bus, schemas.ReviewJob{
		PrRef:   ref,
		Trigger: triggerFor(p.Action),
	})
}

func (g *gateway) routePush(ctx context.Context, p *ports.PushPayload) error {
	if p == nil {
		return nil
	}
	expected := "refs/heads/" + p.Repo.DefaultBranch
	if p.Ref != expected {
		return nil
	}

	repo := p.Repo
	repo.TenantId = g.tenantId
	if err := g.ensureRepo(ctx, repo); err != nil {
		return err
	}

	return schemas.PublishIndexJob(ctx, g.bus, schemas.IndexJob{
		TenantId:  g.tenantId,
		RepoId:    p.Repo.RepoId,
		Ref:       p.Ref,
		BeforeSha: p.BeforeSha,
		HeadSha:   p.HeadSha,
	})
}

func (g *gateway) routeReviewComment(ctx context.Context, p *ports.ReviewCommentPayload) error {
	if p == nil || p.IsBot {
		return nil
	}
	body := strings.TrimSpace(p.Body)
	cmd, rest := parseSlashCommand(body)
	switch cmd {
	case "/review":
		_ = rest // slice 2 ignores args
		ref := p.Ref
		ref.TenantId = g.tenantId
		return schemas.PublishReviewJob(ctx, g.bus, schemas.ReviewJob{
			PrRef:   ref,
			Trigger: ports.TriggerSlashCommand,
		})
	case "/improve", "/ask":
		// Parked for slice 3; tracked so users get a clear log line.
		g.obs.Logger.Info("slash command not yet supported",
			"command", cmd, "pr_number", p.Ref.PrNumber)
		return nil
	}
	// Not a slash command. If this comment is a reply under another
	// comment, treat it as a feedback signal — the worker will look up
	// the parent by InReplyToId and only act if the parent is a bot
	// comment we own.
	if p.InReplyToId != 0 {
		return schemas.PublishFeedbackJob(ctx, g.bus, schemas.FeedbackJob{
			TenantId:          g.tenantId,
			RepoId:            p.Ref.RepoId,
			Kind:              "reply",
			CommentExternalId: p.InReplyToId,
			AuthorId:          p.AuthorId,
		})
	}
	return nil
}

func (g *gateway) routeReaction(ctx context.Context, p *ports.ReactionPayload) error {
	if p == nil || p.CommentExternalId == 0 {
		return nil
	}
	return schemas.PublishFeedbackJob(ctx, g.bus, schemas.FeedbackJob{
		TenantId:          g.tenantId,
		RepoId:            "", // unknown at reaction event time; worker uses CommentExternalId
		Kind:              "reaction",
		CommentExternalId: p.CommentExternalId,
		Reaction:          p.Reaction,
		AuthorId:          p.UserId,
	})
}

func (g *gateway) ensureRepo(ctx context.Context, repo ports.RepoRef) error {
	if g.repos == nil {
		return nil
	}
	if err := g.repos.EnsureExists(ctx, repo); err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}
	return nil
}

func parseSlashCommand(body string) (cmd, rest string) {
	if !strings.HasPrefix(body, "/") {
		return "", ""
	}
	firstLine := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		firstLine = body[:i]
	}
	parts := strings.SplitN(firstLine, " ", 2)
	cmd = strings.ToLower(parts[0])
	if len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}
	return cmd, rest
}

func triggerFor(action string) ports.Trigger {
	if action == "synchronize" {
		return ports.TriggerPrUpdated
	}
	return ports.TriggerPrOpened
}
