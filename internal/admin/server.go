package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"codereviewer/internal/config"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Deps is everything the admin server needs from the composition root.
type Deps struct {
	Cfg      *schemas.Config
	Settings store.SettingsStore
	Comments store.CommentStore
	Rules    store.RuleStore
	PrRuns   store.PrRunStore
	Repos    store.RepoStore
	Context  store.ContextStore
	Bus      ports.MessageBus
	// Pool is the raw pgxpool for export/import of code_chunks +
	// review_comments + rules. Held as `any` so the admin package
	// doesn't import the pgx package directly; the export module
	// type-asserts inside its own file.
	Pool any
	Obs  ports.Obs
}

// Server is the admin web app.
type Server struct {
	deps         Deps
	tmpl         *template.Template
	password     string
	secret       string
	sessionTTL   time.Duration
	secure       bool
	loginLimiter *rateLimiter
}

// New constructs a Server. password and sessionSecret are provided by
// the caller (typically the SecretsProvider). secure=true sets the
// session cookie's Secure flag for HTTPS-only deployments.
func New(deps Deps, password, sessionSecret string, secure bool) (*Server, error) {
	if password == "" {
		return nil, errors.New("admin password is empty (set ADMIN_PASSWORD)")
	}
	if sessionSecret == "" {
		return nil, errors.New("admin session secret is empty (set ADMIN_SESSION_SECRET)")
	}
	t, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	rl := newRateLimiter(
		max(deps.Cfg.RateLimit.LoginAttempts, 1),
		time.Duration(max(deps.Cfg.RateLimit.LoginWindowMinutes, 1))*time.Minute,
	)
	return &Server{
		deps:         deps,
		tmpl:         t,
		password:     password,
		secret:       sessionSecret,
		sessionTTL:   time.Duration(deps.Cfg.Admin.SessionMinutes) * time.Minute,
		secure:       secure,
		loginLimiter: rl,
	}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Router returns the configured chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", s.handleHealth)
	r.Get("/login", s.handleLoginGET)
	r.Post("/login", s.handleLoginPOST)
	r.Post("/logout", s.handleLogout)
	r.Get("/oauth/github", s.handleOAuthStart)
	r.Get("/oauth/github/callback", s.handleOAuthCallback)

	// Authed routes.
	r.Group(func(r chi.Router) {
		r.Use(s.requireSession)
		r.Get("/", s.handleDashboard)
		r.Get("/settings", s.handleSettingsGET)
		r.Post("/settings", s.handleSettingsPOST)
		r.Get("/import", s.handleImportGET)
		r.Post("/import/config", s.handleImportConfigPOST)
		r.Post("/import/db", s.handleImportDbPOST)
		r.Get("/export/config", s.handleExportConfig)
		r.Get("/export/db", s.handleExportDb)
		r.Get("/instructions", s.handleInstructionsGET)
		r.Post("/instructions", s.handleInstructionsPOST)
		r.Get("/pr-context", s.handlePrContextGET)
		r.Post("/pr-context", s.handlePrContextPOST)
		r.Get("/runs", s.handleRunsGET)
		r.Post("/runs/retry", s.handleRunsRetryPOST)
		r.Get("/repos", s.handleReposGET)
		r.Post("/repos/toggle", s.handleReposTogglePOST)
	})

	return r
}

// requireSession is the auth middleware for authed routes.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := readSession(r, s.secret); err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok\n"))
}

type viewData struct {
	Title        string
	Authed       bool
	Subject      string
	FlashOk      string
	FlashErr     string
	OAuthEnabled bool

	ServiceName string
	TenantId    string
	OverlayKeys []kv
	Counts      counts
	Fields      []field
}

// chrome lets renderWith access the embedded viewData on richer view
// payloads via pointer. Implementations call &payload.viewData here.
func (v *viewData) chrome() *viewData { return v }

type kv struct{ Key, Value string }
type counts struct {
	CodeChunks     int
	Rules          int
	ReviewComments int
	PrRuns         int
}
type field struct {
	Key, Value, Help string
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, vd viewData) {
	vd.Title = name
	s.populateChrome(r, &vd)
	s.executeTemplate(w, name, vd)
}

// renderWith is used by pages that need richer view data than the bare
// viewData. The payload type must embed viewData so the layout's
// Title/Authed/Subject/Flash fields resolve via field promotion.
func (s *Server) renderWith(w http.ResponseWriter, r *http.Request, name string, payload interface {
	chrome() *viewData
}) {
	vd := payload.chrome()
	vd.Title = name
	s.populateChrome(r, vd)
	s.executeTemplate(w, name, payload)
}

func (s *Server) populateChrome(r *http.Request, vd *viewData) {
	if sess, err := readSession(r, s.secret); err == nil {
		vd.Authed = true
		vd.Subject = sess.Subject
	}
	vd.OAuthEnabled = s.deps.Cfg.Admin.GithubOAuth.ClientId != ""
}

func (s *Server) executeTemplate(w http.ResponseWriter, name string, data any) {
	tname := name + ".html"
	tmpl, err := template.ParseFS(templatesFS, "templates/layout.html", "templates/"+tname)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.deps.Obs.Logger.Error("template render failed", "err", err.Error())
	}
}

func (s *Server) handleLoginGET(w http.ResponseWriter, r *http.Request) {
	if _, err := readSession(r, s.secret); err == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.render(w, r, "login", viewData{})
}

func (s *Server) handleLoginPOST(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.allow(clientIP(r)) {
		s.render(w, r, "login", viewData{FlashErr: "Too many attempts. Try again in a few minutes."})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form", http.StatusBadRequest)
		return
	}
	submitted := r.PostFormValue("password")
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(s.password)) != 1 {
		s.render(w, r, "login", viewData{FlashErr: "Invalid password."})
		return
	}
	sess := Session{Subject: "password", ExpiresAt: time.Now().Add(s.sessionTTL)}
	setSession(w, sess, s.secret, s.secure)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	vd := viewData{
		ServiceName: s.deps.Cfg.Observability.ServiceName,
		TenantId:    s.deps.Cfg.Tenant.Id,
	}
	for _, k := range config.OverlayKeys {
		vd.OverlayKeys = append(vd.OverlayKeys, kv{Key: k, Value: config.ReadCurrent(s.deps.Cfg, k)})
	}
	vd.Counts = s.gatherCounts(r.Context())
	s.render(w, r, "dashboard", vd)
}

func (s *Server) gatherCounts(ctx context.Context) counts {
	var c counts
	if s.deps.Pool != nil {
		c.CodeChunks = countTable(ctx, s.deps.Pool, "code_chunks")
		c.Rules = countTable(ctx, s.deps.Pool, "rules")
		c.ReviewComments = countTable(ctx, s.deps.Pool, "review_comments")
		c.PrRuns = countTable(ctx, s.deps.Pool, "pr_runs")
	}
	return c
}

// settingHelp is short inline help text per overlay key. Kept here so
// the form layout and the descriptions stay in one file.
var settingHelp = map[string]string{
	"rules.git_url":              "Git URL of the rules repository (https or ssh).",
	"rules.branch":               "Branch to clone (default: main).",
	"tenant.id":                  "Tenant identifier; used as a primary partition key on every row.",
	"tenant.name":                "Human-friendly tenant display name.",
	"cost.daily_usd_cap_default": "Default daily USD spend cap per (tenant, repo); per-repo overrides live in the cost_caps table.",
	"llm.primary_model_url":      "Logical model name routed to the primary tier by LiteLLM.",
	"llm.fallback_model_url":     "Logical model name routed to the fallback tier.",
	"llm.embeddings_url":         "Embeddings model name.",
	"llm.per_pr_token_cap":       "Hard cap on tokens assembled into a single review prompt; the diff is never trimmed.",
	"observability.sink":         "stdout (local) or otel (collector).",
	"observability.otlp_endpoint": "OTLP HTTP endpoint, e.g. otel-collector:4318.",
	"observability.service_name":  "Service name emitted on every span / metric.",
}

func (s *Server) handleSettingsGET(w http.ResponseWriter, r *http.Request) {
	vd := viewData{}
	for _, k := range config.OverlayKeys {
		v, _, _ := s.deps.Settings.Get(r.Context(), k)
		if v == "" {
			v = config.ReadCurrent(s.deps.Cfg, k)
		}
		vd.Fields = append(vd.Fields, field{Key: k, Value: v, Help: settingHelp[k]})
	}
	s.render(w, r, "settings", vd)
}

func (s *Server) handleSettingsPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form", http.StatusBadRequest)
		return
	}
	sess, _ := readSession(r, s.secret)
	for _, k := range config.OverlayKeys {
		v := r.PostFormValue(k)
		// Empty submission deletes the override so the TOML default takes over.
		if v == "" {
			if err := s.deps.Settings.Delete(r.Context(), k); err != nil {
				s.renderError(w, r, "settings", fmt.Errorf("delete %s: %w", k, err))
				return
			}
			continue
		}
		if err := s.deps.Settings.Set(r.Context(), k, v, sess.Subject); err != nil {
			s.renderError(w, r, "settings", fmt.Errorf("set %s: %w", k, err))
			return
		}
	}
	// Re-apply overlay so the in-memory config view is fresh.
	if err := config.ApplyOverlay(r.Context(), s.deps.Cfg, s.deps.Settings); err != nil {
		s.renderError(w, r, "settings", fmt.Errorf("re-apply overlay: %w", err))
		return
	}
	s.renderOk(w, r, "settings", "Settings saved. Restart workers to pick them up.")
}

func (s *Server) handleImportGET(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "import", viewData{})
}

func (s *Server) renderOk(w http.ResponseWriter, r *http.Request, page, msg string) {
	vd := viewData{FlashOk: msg}
	if page == "settings" {
		// Rebuild form data for re-render.
		for _, k := range config.OverlayKeys {
			v, _, _ := s.deps.Settings.Get(r.Context(), k)
			if v == "" {
				v = config.ReadCurrent(s.deps.Cfg, k)
			}
			vd.Fields = append(vd.Fields, field{Key: k, Value: v, Help: settingHelp[k]})
		}
	}
	s.render(w, r, page, vd)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, page string, err error) {
	s.deps.Obs.Logger.Error("admin handler error", "page", page, "err", err.Error())
	s.render(w, r, page, viewData{FlashErr: err.Error()})
}
