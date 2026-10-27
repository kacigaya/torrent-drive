package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2/endpoints"
)

func TestOAuthCallbackChecksHostStateAndCode(t *testing.T) {
	codes := make(chan string, 1)
	handler := authorizationCallback("expected-state", codes)
	for _, target := range []string{
		"http://attacker.example/callback?state=expected-state&code=test",
		"http://127.0.0.1:53682/callback?state=wrong&code=test",
		"http://127.0.0.1:53682/callback?state=expected-state&error=access_denied",
	} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", target, nil))
		if w.Code < 400 || len(codes) != 0 {
			t.Fatal("invalid callback accepted")
		}
	}
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "http://127.0.0.1:53682/callback?state=expected-state&code=test-code", nil))
	if w.Code != 200 || <-codes != "test-code" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("valid callback failed")
	}
}

func TestDesktopClientRequiresInstalledCredentialsAndUsesGoogle(t *testing.T) {
	for _, input := range []string{`{}`, `{"web":{"client_id":"test","client_secret":"test"}}`, `{"installed":{}}`, `not-json`} {
		if _, err := desktopClient([]byte(input)); err == nil {
			t.Fatal("invalid client accepted")
		}
	}
	config, err := desktopClient([]byte(`{"installed":{"client_id":"test-client","client_secret":"test-secret","auth_uri":"https://attacker.example/auth","token_uri":"https://attacker.example/token"}}`))
	if err != nil || config.Endpoint != endpoints.Google || config.ClientID != "test-client" {
		t.Fatal("client did not use trusted Google endpoints", err)
	}
}

func TestAuthorizeNeverOverwritesExistingCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.json")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := authorize(context.Background(), "unused-client.json", path, "127.0.0.1:53682"); err == nil {
		t.Fatal("existing output accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "existing" {
		t.Fatal("credentials overwritten")
	}
}
