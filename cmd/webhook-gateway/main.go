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
	"sync"
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

	// Stores boot from TOML-only config so the runtime overlay table is
	// reachable; everything else then sees the overlayed values.
	stores, err := boot.PickStores(ctx, cfg.Store, ports.Obs{})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if stores.Close != nil {
		defer stores.Close()
	}
	if err := config.ApplyOverlay(ctx, cfg, stores.Settings); err != nil {
		return fmt.Errorf("apply settings overlay: %w", err)
	}

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	reloader, err := boot.NewReloader(*cfg, stores.Settings, 30*time.Second)
	if err != nil {
		return fmt.Errorf("settings reloader: %w", err)
	}
	go reloader.Run(ctx, obs.Logger)
	_ = reloader // available for future live-tunable gateway settings

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	vcs, err := boot.PickVcsRegistry(cfg.Vcs, secrets)
	if err != nil {
		return fmt.Errorf("vcs: %w", err)
	}

	gw := &gateway{
		vcs:       vcs,
		bus:       bus,
		repos:     stores.Repos,
		contextDB: stores.Context,
		obs:       obs,
		tenantId:  ports.TenantId(cfg.Tenant.Id),
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	if cfg.RateLimit.WebhookMaxBodyBytes > 0 {
		r.Use(middleware.RequestSize(int64(cfg.RateLimit.WebhookMaxBodyBytes)))
	}
	webhookLimiter := newWebhookLimiter(cfg.RateLimit.WebhookPerSecond)
	r.Get("/health", gw.health)
	// Each route resolves its own VcsSource from the registry. Providers
	// not configured for this deployment return 404 at request time so
	// stray webhooks fail loudly instead of slipping into the wrong
	// adapter's VerifyWebhook.
	r.With(webhookLimiter).Post("/github/webhook", gw.webhookHandler(ports.VcsProviderGitHub))
	r.With(webhookLimiter).Post("/bitbucket/webhook", gw.webhookHandler(ports.VcsProviderBitbucket))

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

// newWebhookLimiter returns a chi-compatible middleware that rejects
// any IP exceeding perSecond requests per second. Implementation: a
// per-IP token bucket capped at perSecond, refilled once per second.
// Concurrency-safe via sync.Mutex; the contention is fine at the
// expected scale (a few hundred req/sec across all IPs).
func newWebhookLimiter(perSecond int) func(http.Handler) http.Handler {
	if perSecond <= 0 {
		// No-op middleware.
		return func(next http.Handler) http.Handler { return next }
	}
	state := &webhookLimiterState{
		perSecond: perSecond,
		bucket:    make(map[string]*webhookBucket),
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !state.allow(webhookClientIP(r)) {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type webhookLimiterState struct {
	mu        sync.Mutex
	perSecond int
	bucket    map[string]*webhookBucket
}

type webhookBucket struct {
	tokens   int
	lastFill time.Time
}

func (s *webhookLimiterState) allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	b, ok := s.bucket[ip]
	if !ok {
		s.bucket[ip] = &webhookBucket{tokens: s.perSecond - 1, lastFill: now}
		return true
	}
	// Refill: one full bucket per second of elapsed time.
	elapsed := now.Sub(b.lastFill)
	if elapsed >= time.Second {
		b.tokens = s.perSecond
		b.lastFill = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func webhookClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

type gateway struct {
	vcs       ports.VcsRegistry
	bus       ports.MessageBus
	repos     store.RepoStore
	contextDB store.ContextStore
	obs       ports.Obs
	tenantId  ports.TenantId
}

func (g *gateway) health(w http.ResponseWriter, r *http.Request) {
	status, err := g.bus.Health(r.Context())
	if err != nil || !status.Healthy {
		http.Error(w, "unhealthy: "+status.Detail, http.StatusServiceUnavailable)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// webhookHandler builds an http.HandlerFunc bound to a specific VCS
// provider. The handler resolves the matching VcsSource from the
// registry at request time so a deployment running only one adapter
// returns 404 for routes whose provider isn't configured.
func (g *gateway) webhookHandler(provider ports.VcsProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vcs, err := g.vcs.For(provider)
		if err != nil {
			g.obs.Logger.Warn("webhook for unconfigured provider", "provider", string(provider))
			http.Error(w, "provider not configured", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		event, err := vcs.VerifyWebhook(r.Context(), r.Header, body)
		if err != nil {
			g.obs.Logger.Warn("webhook rejected",
				"err", err.Error(),
				"provider", string(provider),
				"delivery", r.Header.Get("X-GitHub-Delivery"),
				"event", r.Header.Get("X-GitHub-Event"),
			)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := g.route(r.Context(), provider, event); err != nil {
			g.obs.Logger.Error("route webhook failed",
				"err", err.Error(),
				"provider", string(provider),
				"kind", string(event.Kind),
				"delivery", event.DeliveryId,
			)
			http.Error(w, "route", http.StatusInternalServerError)
			return
		}
		g.obs.Logger.Info("webhook accepted",
			"provider", string(provider),
			"kind", string(event.Kind),
			"delivery", event.DeliveryId,
		)
		w.WriteHeader(http.StatusAccepted)
	}
}

func (g *gateway) route(ctx context.Context, provider ports.VcsProvider, event ports.WebhookEvent) error {
	switch event.Kind {
	case ports.WebhookKindPullRequest:
		return g.routePullRequest(ctx, provider, event.PullRequest)
	case ports.WebhookKindPush:
		return g.routePush(ctx, provider, event.Push)
	case ports.WebhookKindReviewComment:
		return g.routeReviewComment(ctx, provider, event.ReviewComment)
	case ports.WebhookKindReaction:
		return g.routeReaction(ctx, provider, event.Reaction)
	}
	return nil
}

func (g *gateway) routePullRequest(ctx context.Context, provider ports.VcsProvider, p *ports.PullRequestPayload) error {
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
	repo.Provider = provider
	if err := g.ensureRepo(ctx, repo); err != nil {
		return err
	}

	ref := p.Ref
	ref.TenantId = g.tenantId
	ref.Provider = provider
	return schemas.PublishReviewJob(ctx, g.bus, schemas.ReviewJob{
		PrRef:   ref,
		Trigger: triggerFor(p.Action),
	})
}

func (g *gateway) routePush(ctx context.Context, provider ports.VcsProvider, p *ports.PushPayload) error {
	if p == nil {
		return nil
	}
	expected := "refs/heads/" + p.Repo.DefaultBranch
	if p.Ref != expected {
		return nil
	}

	repo := p.Repo
	repo.TenantId = g.tenantId
	repo.Provider = provider
	if err := g.ensureRepo(ctx, repo); err != nil {
		return err
	}

	return schemas.PublishIndexJob(ctx, g.bus, schemas.IndexJob{
		TenantId:  g.tenantId,
		RepoId:    p.Repo.RepoId,
		Provider:  provider,
		Ref:       p.Ref,
		BeforeSha: p.BeforeSha,
		HeadSha:   p.HeadSha,
	})
}

func (g *gateway) routeReviewComment(ctx context.Context, provider ports.VcsProvider, p *ports.ReviewCommentPayload) error {
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
		ref.Provider = provider
		return schemas.PublishReviewJob(ctx, g.bus, schemas.ReviewJob{
			PrRef:   ref,
			Trigger: ports.TriggerSlashCommand,
		})
	case "/context":
		return g.handleContextCommand(ctx, p, body, rest)
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
			Provider:          provider,
			Kind:              "reply",
			CommentExternalId: p.InReplyToId,
			AuthorId:          p.AuthorId,
			Body:              p.Body,
			PrNumber:          p.Ref.PrNumber,
		})
	}
	return nil
}

// handleContextCommand persists ad-hoc context for the PR. Body is
// the comment after "/context" — either the rest of the same line, or
// the entire body minus the command line. The author becomes the
// created_by attribution.
func (g *gateway) handleContextCommand(ctx context.Context, p *ports.ReviewCommentPayload, fullBody, rest string) error {
	if g.contextDB == nil {
		return nil
	}
	text := strings.TrimSpace(rest)
	if text == "" {
		// Multi-line: take everything after the first \n.
		if i := strings.IndexByte(fullBody, '\n'); i >= 0 {
			text = strings.TrimSpace(fullBody[i+1:])
		}
	}
	if text == "" {
		g.obs.Logger.Info("ignoring empty /context command", "pr_number", p.Ref.PrNumber)
		return nil
	}
	return g.contextDB.AppendPrContext(ctx, store.PrContextItem{
		TenantId:  g.tenantId,
		RepoId:    p.Ref.RepoId,
		PrNumber:  p.Ref.PrNumber,
		Source:    "command",
		Body:      text,
		CreatedBy: p.AuthorId,
	})
}

func (g *gateway) routeReaction(ctx context.Context, provider ports.VcsProvider, p *ports.ReactionPayload) error {
	if p == nil || p.CommentExternalId == 0 {
		return nil
	}
	return schemas.PublishFeedbackJob(ctx, g.bus, schemas.FeedbackJob{
		TenantId:          g.tenantId,
		RepoId:            "", // unknown at reaction event time; worker uses CommentExternalId
		Provider:          provider,
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
