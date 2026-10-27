// Package auth runs the one-time Google OAuth flow and saves a refresh token.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kacigaya/torrent-drive/internal/drive"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

func desktopClient(data []byte) (*oauth2.Config, error) {
	var document struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
	}
	if json.Unmarshal(data, &document) != nil || document.Installed == nil {
		return nil, errors.New("download an OAuth client JSON with Desktop app type from Google Cloud")
	}
	if document.Installed.ClientID == "" || document.Installed.ClientSecret == "" {
		return nil, errors.New("invalid Desktop OAuth client JSON")
	}
	return &oauth2.Config{ClientID: document.Installed.ClientID, ClientSecret: document.Installed.ClientSecret, Endpoint: endpoints.Google, Scopes: []string{"https://www.googleapis.com/auth/drive"}}, nil
}

// Authorize prints a Google consent URL, waits for the loopback callback,
// and writes the credential to output. It never overwrites a file.
func Authorize(parent context.Context, clientPath, output, address string) error {
	if clientPath == "" {
		return errors.New("provide --client /path/to/client.json")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("OAuth output already exists or cannot be checked; choose a new --oauth path")
	}
	data, err := os.ReadFile(clientPath)
	if err != nil {
		return errors.New("cannot read OAuth client JSON")
	}
	config, err := desktopClient(data)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || (host != "127.0.0.1" && host != "0.0.0.0") || port != "53682" {
		return errors.New("callback must use 127.0.0.1:53682 or container-only 0.0.0.0:53682")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("cannot bind OAuth callback port 53682")
	}
	defer listener.Close()
	config.RedirectURL = "http://127.0.0.1:53682/callback"
	stateBytes := make([]byte, 32)
	if _, err = rand.Read(stateBytes); err != nil {
		return err
	}
	state, verifier := hex.EncodeToString(stateBytes), oauth2.GenerateVerifier()
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", authorizationCallback(state, codes))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	fmt.Println("Open this URL in your local browser. Keep this terminal running:")
	fmt.Println(config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)))
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	var code string
	select {
	case code = <-codes:
	case <-ctx.Done():
		return errors.New("OAuth setup cancelled or timed out")
	}
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil || token.RefreshToken == "" {
		return errors.New("Google authorization failed or returned no refresh token; repeat setup and grant offline access")
	}
	encoded, err := json.Marshal(drive.Credential{ClientID: config.ClientID, ClientSecret: config.ClientSecret, Token: *token})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create private OAuth output file")
	}
	if _, err = file.Write(encoded); err != nil {
		file.Close()
		os.Remove(output)
		return errors.New("cannot write OAuth output")
	}
	err = file.Close()
	if err == nil {
		fmt.Println("OAuth connection saved. No tokens were printed.")
	}
	return err
}

func authorizationCallback(state string, codes chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.Host != "127.0.0.1:53682" || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth callback.", http.StatusForbidden)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" || len(code) > 8192 {
			http.Error(w, "Google did not provide an authorization code. Restart setup to try again.", http.StatusBadRequest)
			return
		}
		select {
		case codes <- code:
			fmt.Fprintln(w, "Authorization received. Return to your terminal to check the result.")
		default:
			http.Error(w, "Authorization already received.", http.StatusConflict)
		}
	}
}
