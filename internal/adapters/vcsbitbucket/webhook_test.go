package vcsbitbucket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	secret := []byte("super-secret")
	body := []byte(`{"event":"ping"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	cases := []struct {
		name    string
		headers http.Header
		body    []byte
		wantErr bool
	}{
		{
			name:    "valid signature",
			headers: http.Header{"X-Hub-Signature": []string{good}},
			body:    body,
			wantErr: false,
		},
		{
			name:    "valid via alternate header name",
			headers: http.Header{"X-Event-Hub-Signature": []string{good}},
			body:    body,
			wantErr: false,
		},
		{
			name:    "missing header",
			headers: http.Header{},
			body:    body,
			wantErr: true,
		},
		{
			name:    "wrong prefix",
			headers: http.Header{"X-Hub-Signature": []string{"sha1=" + hex.EncodeToString(mac.Sum(nil))}},
			body:    body,
			wantErr: true,
		},
		{
			name:    "wrong digest",
			headers: http.Header{"X-Hub-Signature": []string{"sha256=deadbeef"}},
			body:    body,
			wantErr: true,
		},
		{
			name:    "body tampered",
			headers: http.Header{"X-Hub-Signature": []string{good}},
			body:    []byte(`{"event":"ping","extra":true}`),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifySignature(tc.headers, tc.body, secret)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
