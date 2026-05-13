package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oauthStateCookieName carries the CSRF state for an in-flight OAuth
// handshake. We sign it with the same secret as the session cookie so
// no extra crypto key needs to be configured.
const oauthStateCookieName = "codereviewer_admin_oauth_state"

// generateOAuthState returns a 32-byte URL-safe random token.
func generateOAuthState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// handleOAuthStart sends the user to GitHub's authorize page.
//
// Route: GET /oauth/github
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Cfg.Admin.GithubOAuth
	if cfg.ClientId == "" {
		http.Error(w, "GitHub OAuth is not configured", http.StatusNotFound)
		return
	}
	state, err := generateOAuthState()
	if err != nil {
		http.Error(w, "state token error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state,
		Path:     "/oauth/github",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secure,
		Expires:  time.Now().Add(10 * time.Minute),
	})

	q := url.Values{}
	q.Set("client_id", cfg.ClientId)
	q.Set("redirect_uri", cfg.CallbackURL)
	q.Set("scope", "read:org")
	q.Set("state", state)
	q.Set("allow_signup", "false")

	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+q.Encode(), http.StatusFound)
}

// handleOAuthCallback finishes the handshake. Verifies state, swaps the
// `code` for an access token, fetches the authenticated user's orgs,
// and signs them in if any org is in AllowedOrgs.
//
// Route: GET /oauth/github/callback
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Cfg.Admin.GithubOAuth
	if cfg.ClientId == "" {
		http.Error(w, "GitHub OAuth is not configured", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	got := q.Get("state")
	c, err := r.Cookie(oauthStateCookieName)
	if err != nil || c.Value == "" || c.Value != got {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	// Single-use: clear the state cookie now.
	http.SetCookie(w, &http.Cookie{
		Name:    oauthStateCookieName,
		Value:   "",
		Path:    "/oauth/github",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := exchangeOAuthCode(r.Context(), cfg.ClientId, cfg.ClientSecret, cfg.CallbackURL, code)
	if err != nil {
		s.deps.Obs.Logger.Warn("oauth token exchange failed", "err", err.Error())
		http.Error(w, "oauth exchange failed", http.StatusBadGateway)
		return
	}

	login, err := fetchGithubLogin(r.Context(), token)
	if err != nil {
		s.deps.Obs.Logger.Warn("oauth user lookup failed", "err", err.Error())
		http.Error(w, "oauth user lookup failed", http.StatusBadGateway)
		return
	}

	if len(cfg.AllowedOrgs) > 0 {
		orgs, err := fetchGithubOrgs(r.Context(), token)
		if err != nil {
			s.deps.Obs.Logger.Warn("oauth org lookup failed", "err", err.Error())
			http.Error(w, "org check failed", http.StatusForbidden)
			return
		}
		if !anyMatch(orgs, cfg.AllowedOrgs) {
			s.deps.Obs.Logger.Warn("oauth org check denied", "login", login, "orgs", orgs)
			http.Error(w, "not a member of an allowed org", http.StatusForbidden)
			return
		}
	}

	sess := Session{Subject: "github:" + login, ExpiresAt: time.Now().Add(s.sessionTTL)}
	setSession(w, sess, s.secret, s.secure)
	http.Redirect(w, r, "/", http.StatusFound)
}

func exchangeOAuthCode(ctx context.Context, clientId, clientSecret, redirectURI, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientId)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", redirectURI)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth exchange http %d: %s", res.StatusCode, string(body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.Error != "" || payload.AccessToken == "" {
		return "", fmt.Errorf("oauth exchange: %s", payload.Error)
	}
	return payload.AccessToken, nil
}

func fetchGithubLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github /user http %d", res.StatusCode)
	}
	var payload struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Login == "" {
		return "", errors.New("github /user returned empty login")
	}
	return payload.Login, nil
}

func fetchGithubOrgs(ctx context.Context, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/user/orgs?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github /user/orgs http %d", res.StatusCode)
	}
	var payload []struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(payload))
	for _, o := range payload {
		out = append(out, o.Login)
	}
	return out, nil
}

func anyMatch(have, allowed []string) bool {
	want := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		want[strings.ToLower(a)] = struct{}{}
	}
	for _, h := range have {
		if _, ok := want[strings.ToLower(h)]; ok {
			return true
		}
	}
	return false
}
