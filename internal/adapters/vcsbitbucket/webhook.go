package vcsbitbucket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// verifySignature returns an error iff the X-Hub-Signature header on a
// Bitbucket Cloud webhook delivery doesn't match HMAC-SHA256(secret, body).
//
// Bitbucket Cloud follows GitHub's convention since 2024: the header is
// "X-Hub-Signature" with value "sha256=<lowercase-hex>". Older Bitbucket
// Cloud webhooks did not sign payloads at all — this adapter refuses
// unsigned deliveries; configure the webhook with a secret in the UI.
func verifySignature(headers http.Header, body, secret []byte) error {
	sig := headers.Get("X-Hub-Signature")
	if sig == "" {
		// Bitbucket sometimes uses X-Event-Hub-Signature; accept either
		// header name to insulate us from minor docs drift.
		sig = headers.Get("X-Event-Hub-Signature")
	}
	if sig == "" {
		return fmt.Errorf("missing X-Hub-Signature header (webhook secret not configured?)")
	}
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return fmt.Errorf("X-Hub-Signature missing sha256= prefix")
	}
	wantHex := sig[len(prefix):]
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		return fmt.Errorf("X-Hub-Signature not valid hex: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(want, mac.Sum(nil)) {
		return fmt.Errorf("X-Hub-Signature does not match HMAC of body")
	}
	return nil
}
