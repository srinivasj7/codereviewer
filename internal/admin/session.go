// Package admin holds the web UI handlers, session middleware, and
// import/export logic for cmd/admin-ui. Templates are embedded via
// go:embed so the binary ships standalone.
package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionCookieName is the cookie that carries the signed identity.
const sessionCookieName = "codereviewer_admin"

// Session is the decoded payload from the signed cookie.
type Session struct {
	Subject   string    // "password" or "github:<login>"
	ExpiresAt time.Time
}

// signSession returns a base64(payload|mac) cookie value. payload is
// "<sub>|<unix-seconds>"; mac is HMAC-SHA256(secret, payload).
func signSession(s Session, secret string) string {
	payload := fmt.Sprintf("%s|%d", s.Subject, s.ExpiresAt.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// verifySession decodes and validates a cookie value. Returns error if
// the signature is wrong or the session has expired.
func verifySession(cookieValue, secret string) (Session, error) {
	parts := strings.SplitN(cookieValue, ".", 2)
	if len(parts) != 2 {
		return Session{}, errors.New("malformed cookie")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, fmt.Errorf("decode payload: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, fmt.Errorf("decode signature: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payloadBytes)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, sigBytes) {
		return Session{}, errors.New("bad signature")
	}
	subAndExp := strings.SplitN(string(payloadBytes), "|", 2)
	if len(subAndExp) != 2 {
		return Session{}, errors.New("malformed payload")
	}
	exp, err := strconv.ParseInt(subAndExp[1], 10, 64)
	if err != nil {
		return Session{}, fmt.Errorf("parse expiry: %w", err)
	}
	s := Session{Subject: subAndExp[0], ExpiresAt: time.Unix(exp, 0)}
	if time.Now().After(s.ExpiresAt) {
		return Session{}, errors.New("session expired")
	}
	return s, nil
}

// setSession writes the signed cookie. secure=true forces HTTPS-only.
func setSession(w http.ResponseWriter, s Session, secret string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(s, secret),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  s.ExpiresAt,
	})
}

// clearSession overwrites the cookie with an expired one.
func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
}

// readSession extracts and verifies the cookie from r. Returns error on
// missing / malformed / expired sessions.
func readSession(r *http.Request, secret string) (Session, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, err
	}
	return verifySession(c.Value, secret)
}
