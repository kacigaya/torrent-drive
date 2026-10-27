package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func dashboardBackend(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "td-web-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "worker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return socket
}

func webRequest(t *testing.T, handler http.Handler, method, path string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, DefaultOrigin+path, strings.NewReader(body))
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDashboardProxyLimitsRoutesAndStripsBrowserHeaders(t *testing.T) {
	socket := dashboardBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "" {
			t.Error("browser credentials reached the worker")
		}
		if r.URL.Path != "/jobs" || r.URL.RawQuery != "" {
			t.Error("incorrect worker route", r.URL.String())
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.WriteHeader(201)
			w.Write(body)
		} else {
			io.WriteString(w, "[]")
		}
	}))
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: socket})
	if err != nil {
		t.Fatal(err)
	}
	response := webRequest(t, handler, "GET", "/api/jobs", map[string]string{"Cookie": "private", "Authorization": "Bearer private"}, "")
	if response.Code != 200 || response.Body.String() != "[]" {
		t.Fatal("job list failed", response.Code, response.Body.String())
	}
	headers := map[string]string{"Origin": DefaultOrigin, "Content-Type": "application/json", "X-Torrent-Drive": "1", "Cookie": "private"}
	response = webRequest(t, handler, "POST", "/api/jobs", headers, `{"folder":"abc","magnet":"private-source"}`)
	if response.Code != 201 || !strings.Contains(response.Body.String(), "private-source") {
		t.Fatal("submission did not reach worker", response.Code)
	}
	for _, path := range []string{"/api/import-qbittorrent", "/api/jobs/../folders", "/api/jobs?secret=value", "/api/folders?parent=root&unexpected=value", "/api/folders?parent=root&parent=other", "/api/jobs/invalid/pause", "/api/jobs/" + strings.Repeat("a", 32) + "/delete"} {
		if r := webRequest(t, handler, "GET", path, nil, ""); r.Code != 404 {
			t.Fatalf("unlisted route %s returned %d", path, r.Code)
		}
	}
}

func TestDashboardRejectsCrossSiteWritesAndHostSpoofing(t *testing.T) {
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: "/nonexistent/socket"})
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []map[string]string{
		{},
		{"Origin": "https://attacker.example", "Content-Type": "application/json", "X-Torrent-Drive": "1"},
		{"Origin": DefaultOrigin, "Content-Type": "text/plain", "X-Torrent-Drive": "1"},
		{"Origin": DefaultOrigin, "Content-Type": "application/json"},
		{"Origin": DefaultOrigin, "Content-Type": "application/json", "X-Torrent-Drive": "1", "Sec-Fetch-Site": "cross-site"},
	} {
		if r := webRequest(t, handler, "POST", "/api/jobs", headers, "{}"); r.Code != 403 {
			t.Fatalf("cross-site write returned %d", r.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://attacker.example/", nil)
	r.Header.Set("X-Forwarded-Host", "dashboard.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 421 {
		t.Fatal("spoofed host accepted")
	}
	if r := webRequest(t, handler, "GET", "/api/jobs", map[string]string{"Sec-Fetch-Site": "same-site"}, ""); r.Code != 403 {
		t.Fatal("cross-origin sibling site accepted")
	}
}

func TestDashboardExternalNavigation(t *testing.T) {
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: "/unused/socket"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, path, site, mode, destination string
		want                                        int
	}{
		{"external link", "GET", "/", "cross-site", "navigate", "document", 200},
		{"sibling site link", "GET", "/", "same-site", "navigate", "document", 200},
		{"external fetch", "GET", "/", "cross-site", "cors", "empty", 403},
		{"embedded page", "GET", "/", "cross-site", "navigate", "iframe", 403},
		{"missing navigation metadata", "GET", "/", "cross-site", "", "", 403},
		{"API document navigation", "GET", "/api/jobs", "cross-site", "navigate", "document", 403},
		{"API fetch", "GET", "/api/jobs", "cross-site", "cors", "empty", 403},
		{"external form", "POST", "/", "cross-site", "navigate", "document", 403},
		{"API write", "POST", "/api/jobs", "cross-site", "navigate", "document", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := map[string]string{"Sec-Fetch-Site": test.site, "Sec-Fetch-Mode": test.mode, "Sec-Fetch-Dest": test.destination, "Origin": DefaultOrigin, "Content-Type": "application/json", "X-Torrent-Drive": "1"}
			response := webRequest(t, handler, test.method, test.path, headers, "")
			if response.Code != test.want {
				t.Fatalf("got HTTP %d, want %d", response.Code, test.want)
			}
			if test.want == 200 && !strings.Contains(response.Body.String(), "<title>Torrent Drive</title>") {
				t.Fatal("navigation did not return the dashboard")
			}
		})
	}
}

func TestDashboardReturnsSafeUnavailableAndBodyLimitErrors(t *testing.T) {
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: "/private/worker/not-found.sock"})
	if err != nil {
		t.Fatal(err)
	}
	w := webRequest(t, handler, "GET", "/api/jobs", nil, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), "/private/") {
		t.Fatal("unsafe worker failure", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("POST", DefaultOrigin+"/api/jobs", strings.NewReader("{}"))
	r.ContentLength = 13 << 20
	r.Header.Set("Origin", DefaultOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Torrent-Drive", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("oversized metadata accepted")
	}
}

func TestDashboardPrivateDocumentMetadataAndAssets(t *testing.T) {
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: "/unused/socket"})
	if err != nil {
		t.Fatal(err)
	}
	w := webRequest(t, handler, "GET", "/", nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, text := range []string{"Skip to content", "id=\"main-content\"", "<title>Torrent Drive</title>", "noindex, nofollow, nosnippet", "/_next/static/"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatal("missing page contract", text)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private page security headers missing")
	}
	for _, path := range []string{"/favicon.svg", "/robots.txt"} {
		if r := webRequest(t, handler, "GET", path, nil, ""); r.Code != 200 {
			t.Fatal("missing asset", path)
		}
	}
	for _, path := range []string{"/web.go", "/dist/index.html", "/index.html", "/package.json", "/.env", "/_next/static/../index.html", "/_next/static/%2e%2e/index.html"} {
		if r := webRequest(t, handler, "GET", path, nil, ""); r.Code != 404 {
			t.Fatal("unlisted asset exposed", path)
		}
	}
	served := 0
	fs.WalkDir(assets, "dist/_next/static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if !entry.IsDir() && (strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css")) {
			response := webRequest(t, handler, "GET", strings.TrimPrefix(path, "dist"), nil, "")
			if response.Code != 200 || response.Body.Len() == 0 || !strings.Contains(response.Header().Get("Content-Type"), map[bool]string{true: "javascript", false: "css"}[strings.HasSuffix(path, ".js")]) {
				t.Fatal("Next asset not served", path, response.Code)
			}
			served++
		}
		return nil
	})
	if served < 2 {
		t.Fatal("Next production export missing")
	}
}

func TestDashboardAllowsOnlyBuildHashedInlineScripts(t *testing.T) {
	handler, err := Handler(Config{Origin: DefaultOrigin, WorkerSocket: "/unused/socket"})
	if err != nil {
		t.Fatal(err)
	}
	response := webRequest(t, handler, "GET", "/", nil, "")
	policy := response.Header().Get("Content-Security-Policy")
	if strings.Contains(policy, "unsafe-inline") || strings.Contains(policy, "unsafe-eval") {
		t.Fatal("dashboard permits untrusted inline scripts")
	}
	scripts := regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`).FindAllSubmatch(response.Body.Bytes(), -1)
	inline := 0
	for _, script := range scripts {
		if len(script[1]) == 0 {
			continue
		}
		inline++
		hash := sha256.Sum256(script[1])
		if !strings.Contains(policy, "'sha256-"+base64.StdEncoding.EncodeToString(hash[:])+"'") {
			t.Fatal("Next bootstrap script blocked by CSP")
		}
	}
	if inline == 0 {
		t.Fatal("Next bootstrap scripts missing")
	}
	unlisted := sha256.Sum256([]byte("alert(1)"))
	if strings.Contains(policy, "'sha256-"+base64.StdEncoding.EncodeToString(unlisted[:])+"'") {
		t.Fatal("unlisted script permitted")
	}
}

func TestDashboardPolicyHashesOnlyRenderedStyleAttributes(t *testing.T) {
	policy := dashboardPolicy([]byte(`<input style="width:1px;content:&quot;x&quot;"><span style="width:1px;content:&quot;x&quot;">`))
	style := policy[strings.Index(policy, "style-src"):]
	style = style[:strings.Index(style, ";")]
	rendered := sha256.Sum256([]byte(`width:1px;content:"x"`))
	if !strings.Contains(style, "'unsafe-hashes'") || !strings.Contains(style, "'sha256-"+base64.StdEncoding.EncodeToString(rendered[:])+"'") {
		t.Fatalf("rendered style attribute blocked: %s", style)
	}
	if strings.Count(style, "'sha256-") != 2 || strings.Contains(style, "unsafe-inline") {
		t.Fatalf("style policy is too broad: %s", style)
	}
	if plain := dashboardPolicy([]byte(`<p>no styles</p>`)); strings.Contains(plain, "unsafe-hashes") {
		t.Fatal("unsafe-hashes added without style attributes")
	}
}

func TestBaseUIScrollbarStyleMatchesBundle(t *testing.T) {
	// Base UI builds the rule from a class name constant. Check both halves so an
	// upstream change fails here instead of as a silent CSP block in the browser.
	chunks, err := fs.Glob(assets, "dist/_next/static/chunks/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range chunks {
		data, _ := assets.ReadFile(name)
		if bytes.Contains(data, []byte(`"base-ui-disable-scrollbar"`)) && bytes.Contains(data, []byte("{scrollbar-width:none}.${")) && bytes.Contains(data, []byte("}::-webkit-scrollbar{display:none}`")) {
			return
		}
	}
	t.Fatal("Base UI scrollbar style changed; update baseUIScrollbarStyle")
}

func TestDashboardNeverBindsPublicTCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Serve(ctx, Config{Origin: DefaultOrigin, Listen: "0.0.0.0:0"}); err == nil {
		t.Fatal("public TCP listener accepted")
	}
}

func TestRuntimeSettingsAreValidatedAndPrivate(t *testing.T) {
	config := Config{Origin: DefaultOrigin, WorkerSocket: "/unused", DefaultFolder: "example-folder", QBittorrentURL: "https://qbit.example.com/"}
	handler, err := Handler(config)
	if err != nil {
		t.Fatal(err)
	}
	response := webRequest(t, handler, "GET", "/api/config", nil, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"default_folder":"example-folder"`) || strings.Contains(response.Body.String(), "/unused") {
		t.Fatal("runtime settings invalid or leaked a private path", response.Body.String())
	}
	for _, path := range []string{"/api/config?extra=value", "/api/config?secret=value"} {
		if r := webRequest(t, handler, "GET", path, nil, ""); r.Code != 404 {
			t.Fatal("unexpected settings query accepted")
		}
	}
	if r := webRequest(t, handler, "GET", "/api/config", map[string]string{"Sec-Fetch-Site": "cross-site"}, ""); r.Code != 403 {
		t.Fatal("cross-site settings access accepted")
	}
	for _, url := range []string{"javascript:alert(1)", "https://user:secret@qbit.example.com/", "https://qbit.example.com/?token=secret"} {
		config.QBittorrentURL = url
		if _, err := Handler(config); err == nil {
			t.Fatal("unsafe qBittorrent URL accepted")
		}
	}
	config.QBittorrentURL = ""
	config.DefaultFolder = "root"
	if _, err := Handler(config); err == nil {
		t.Fatal("invalid default destination accepted")
	}
}
