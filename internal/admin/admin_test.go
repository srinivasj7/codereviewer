package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/schemas"
	"codereviewer/internal/testing/fakes"
)

const (
	testPassword = "letmein"
	testSecret   = "test-session-secret-must-be-long"
)

func minimalValidConfig(t *testing.T) *schemas.Config {
	t.Helper()
	c := &schemas.Config{
		Vcs:           schemas.VcsConfig{Provider: "github"},
		MessageBus:    schemas.MessageBusConfig{Type: "memory"},
		Store:         schemas.StoreConfig{Type: "memory"},
		Llm:           schemas.LlmConfig{Provider: "litellm", PerPrTokenCap: 30000},
		Secrets:       schemas.SecretsConfig{Provider: "env"},
		Observability: schemas.ObservabilityConfig{Sink: "stdout", ServiceName: "test"},
		Cost:          schemas.CostConfig{DailyUsdCapDefault: 5.0},
		Rules:         schemas.RulesConfig{Branch: "main"},
		Tenant:        schemas.TenantConfig{Id: "default-tenant", Name: "default"},
	}
	require.NoError(t, c.Validate())
	return c
}

func newTestServer(t *testing.T) (*Server, *fakes.SettingsStore) {
	t.Helper()
	settings := fakes.NewSettingsStore()
	cfg := minimalValidConfig(t)
	cfg.Admin.SessionMinutes = 10
	srv, err := New(Deps{
		Cfg:      cfg,
		Settings: settings,
		Obs:      obsstdout.New("test-admin"),
	}, testPassword, testSecret, false)
	require.NoError(t, err)
	return srv, settings
}

func TestSession_RoundTrip(t *testing.T) {
	s := Session{Subject: "password", ExpiresAt: time.Now().Add(5 * time.Minute)}
	cookie := signSession(s, testSecret)
	got, err := verifySession(cookie, testSecret)
	require.NoError(t, err)
	require.Equal(t, "password", got.Subject)
}

func TestSession_Tampered(t *testing.T) {
	s := Session{Subject: "password", ExpiresAt: time.Now().Add(5 * time.Minute)}
	cookie := signSession(s, testSecret)
	// Flip one char of the payload.
	tampered := strings.Replace(cookie, "password", "admin000", 1)
	if tampered == cookie {
		// Replace might have changed nothing if "password" was inside the base64
		// representation differently — flip a byte directly.
		tampered = "X" + cookie[1:]
	}
	_, err := verifySession(tampered, testSecret)
	require.Error(t, err)
}

func TestSession_Expired(t *testing.T) {
	s := Session{Subject: "password", ExpiresAt: time.Now().Add(-1 * time.Minute)}
	cookie := signSession(s, testSecret)
	_, err := verifySession(cookie, testSecret)
	require.Error(t, err)
}

func TestSession_WrongSecret(t *testing.T) {
	s := Session{Subject: "password", ExpiresAt: time.Now().Add(5 * time.Minute)}
	cookie := signSession(s, testSecret)
	_, err := verifySession(cookie, "other-secret")
	require.Error(t, err)
}

func TestLogin_HappyPath(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	form := strings.NewReader("password=" + testPassword)
	r := httptest.NewRequest(http.MethodPost, "/login", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Router().ServeHTTP(w, r)

	require.Equal(t, http.StatusFound, w.Code)
	cookies := w.Result().Cookies()
	var sess *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sess = c
		}
	}
	require.NotNil(t, sess, "session cookie must be set")
	require.NotEmpty(t, sess.Value)
}

func TestLogin_BadPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	form := strings.NewReader("password=wrong")
	r := httptest.NewRequest(http.MethodPost, "/login", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Router().ServeHTTP(w, r)
	// Login form re-renders with status 200 (no redirect).
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Invalid password")
}

func TestRequireSession_RedirectsUnauthed(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	srv.Router().ServeHTTP(w, r)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/login", w.Header().Get("Location"))
}

func TestRequireSession_AllowsAuthed(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	r.AddCookie(&http.Cookie{
		Name: sessionCookieName,
		Value: signSession(Session{
			Subject:   "password",
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}, testSecret),
	})
	srv.Router().ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Settings")
}

func TestNew_RejectsEmptyPassword(t *testing.T) {
	_, err := New(Deps{Cfg: &schemas.Config{}}, "", "secret", false)
	require.Error(t, err)
}

func TestNew_RejectsEmptySessionSecret(t *testing.T) {
	_, err := New(Deps{Cfg: &schemas.Config{}}, "pw", "", false)
	require.Error(t, err)
}

func TestSettings_POST_PersistsAndRedirects(t *testing.T) {
	srv, settings := newTestServer(t)
	authedCookie := &http.Cookie{
		Name: sessionCookieName,
		Value: signSession(Session{
			Subject:   "password",
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}, testSecret),
	}

	form := "rules.git_url=https://new-rules&rules.branch=&cost.daily_usd_cap_default=2.5"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(authedCookie)
	srv.Router().ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Settings saved")

	got, found, _ := settings.Get(r.Context(), "rules.git_url")
	require.True(t, found)
	require.Equal(t, "https://new-rules", got)

	// Empty value deletes the override.
	_, found, _ = settings.Get(r.Context(), "rules.branch")
	require.False(t, found)
}

func TestExport_DataKindNoPool(t *testing.T) {
	srv, _ := newTestServer(t)
	authedCookie := &http.Cookie{
		Name: sessionCookieName,
		Value: signSession(Session{
			Subject:   "password",
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}, testSecret),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/db", nil)
	r.AddCookie(authedCookie)
	srv.Router().ServeHTTP(w, r)
	// No pool injected -> handler responds 500.
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestExportConfig_RoundTrip(t *testing.T) {
	cfg := minimalValidConfig(t)
	cfg.Llm.PerPrTokenCap = 7777
	cfg.Cost.DailyUsdCapDefault = 3.3
	cfg.Rules.GitURL = "https://r"
	snap, err := ExportConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, "https://r", snap.Settings["rules.git_url"])
	require.Equal(t, "7777", snap.Settings["llm.per_pr_token_cap"])

	// ImportConfig writes each value into the SettingsStore.
	settings := fakes.NewSettingsStore()
	require.NoError(t, ImportConfig(context.Background(), snap, settings, "test"))
	got, found, _ := settings.Get(context.Background(), "rules.git_url")
	require.True(t, found)
	require.Equal(t, "https://r", got)
}

func TestImportConfig_RejectsUnknownKey(t *testing.T) {
	snap := ConfigSnapshot{
		Kind:     SnapshotConfig,
		Version:  1,
		Settings: map[string]string{"not.a.real.key": "x"},
	}
	settings := fakes.NewSettingsStore()
	err := ImportConfig(context.Background(), snap, settings, "test")
	require.Error(t, err)
}

func TestImportData_RejectsWrongKind(t *testing.T) {
	err := ImportData(context.Background(), nil, DataSnapshot{Kind: SnapshotConfig})
	require.Error(t, err)
}

func TestDataSnapshot_RoundTripsParentTables(t *testing.T) {
	// Construct a snapshot and marshal/unmarshal it through JSON to
	// confirm the parent-table rows survive serialization in the shape
	// the import path expects.
	snap := DataSnapshot{
		Kind:    SnapshotData,
		Version: 1,
		Tenants: []tenantRow{{TenantId: "default-tenant", Name: "default"}},
		Repos: []repoRow{{
			RepoId: "octo/repo", TenantId: "default-tenant",
			Owner: "octo", Name: "repo", DefaultBranch: "main",
			BackfillWindowDays: 270, Enabled: true,
		}},
		Comments: []commentRow{{
			CommentId: "c1", TenantId: "default-tenant", RepoId: "octo/repo",
			PrNumber: 1, Source: "human", CommentText: "x", Outcome: "pending",
		}},
	}
	b, err := json.Marshal(snap)
	require.NoError(t, err)
	var got DataSnapshot
	require.NoError(t, json.Unmarshal(b, &got))
	require.Len(t, got.Tenants, 1)
	require.Equal(t, "default-tenant", got.Tenants[0].TenantId)
	require.Len(t, got.Repos, 1)
	require.Equal(t, "octo/repo", got.Repos[0].RepoId)
	require.Equal(t, 270, got.Repos[0].BackfillWindowDays)
	require.True(t, got.Repos[0].Enabled)
}

func TestVectorLiteral(t *testing.T) {
	require.Equal(t, "[]", vectorLiteral(nil))
	require.Equal(t, "[]", vectorLiteral([]float32{}))
	require.Equal(t, "[0.5,1.25,-2]", vectorLiteral([]float32{0.5, 1.25, -2}))
}

func TestPgvectorScanner(t *testing.T) {
	var p pgvectorScanner
	require.NoError(t, p.Scan(nil))
	require.Empty(t, p.Slice())

	require.NoError(t, p.Scan("[0.1,0.2,0.3]"))
	require.Len(t, p.Slice(), 3)
	require.InDelta(t, float32(0.2), p.Slice()[1], 1e-6)

	require.Error(t, p.Scan("not-a-vector"))
}
