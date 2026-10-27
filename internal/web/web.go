// Package web serves the dashboard and proxies an allowlisted API to the worker.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kacigaya/torrent-drive/internal/drive"
	"github.com/kacigaya/torrent-drive/internal/jobs"
)

// dist is the static Next.js export. Build it with `bun run build` in frontend/.
//
//go:embed all:dist
var assets embed.FS

// DefaultOrigin is the browser origin for local development.
const DefaultOrigin = "http://127.0.0.1:8080"

type Config struct {
	Origin, WorkerSocket, Listen, Socket string
	DefaultFolder, QBittorrentURL        string
	ContainerListen                      bool
}

func webError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{message})
}

// Handler validates config and returns the dashboard handler.
func Handler(config Config) (http.Handler, error) {
	origin, workerSocket := config.Origin, config.WorkerSocket
	public, err := url.Parse(origin)
	if err != nil || public.Host == "" || public.User != nil || public.RawQuery != "" || public.Fragment != "" || public.Path != "" {
		return nil, errors.New("dashboard origin must be a full origin without a path")
	}
	if public.Scheme != "https" && !(public.Scheme == "http" && net.ParseIP(public.Hostname()).IsLoopback()) {
		return nil, errors.New("dashboard requires HTTPS or a loopback development origin")
	}
	if config.DefaultFolder != "" && (!drive.ValidID(config.DefaultFolder) || config.DefaultFolder == "root") {
		return nil, errors.New("default folder must be a Google Drive folder ID")
	}
	if config.QBittorrentURL != "" {
		qbit, err := url.Parse(config.QBittorrentURL)
		if err != nil || qbit.Host == "" || qbit.User != nil || (qbit.Scheme != "http" && qbit.Scheme != "https") || qbit.RawQuery != "" || qbit.Fragment != "" {
			return nil, errors.New("qBittorrent link must be an HTTP(S) URL without credentials or query parameters")
		}
	}
	page, err := assets.ReadFile("dist/index.html")
	if err != nil {
		return nil, err
	}
	policy := dashboardPolicy(page)
	backend := &url.URL{Scheme: "http", Host: "worker"}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(backend)
			p.Out.Host = "local"
			p.Out.URL.Path = strings.TrimPrefix(p.In.URL.Path, "/api")
			p.Out.URL.RawPath = ""
			p.Out.Header = http.Header{"Accept": {"application/json"}}
			if p.In.Method == http.MethodPost {
				p.Out.Header.Set("Content-Type", "application/json")
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", workerSocket)
			},
			MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeout: 15 * time.Second,
			ResponseHeaderTimeout: 40 * time.Second,
		},
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			webError(w, http.StatusServiceUnavailable, "The downloader is restarting or unavailable. Progress will return when it reconnects.")
		},
	}
	busy := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, nosnippet")
		w.Header().Set("Content-Security-Policy", policy)
		if r.Host != public.Host {
			webError(w, http.StatusMisdirectedRequest, "Unexpected dashboard host.")
			return
		}
		// A link from another site may open the app, but cannot read its API.
		dashboardNavigation := r.Method == http.MethodGet && r.URL.Path == "/" &&
			r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" && !dashboardNavigation {
			webError(w, http.StatusForbidden, "Open the dashboard directly to use it.")
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/config" && r.URL.RawQuery == "" && r.URL.RawPath == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(struct {
				DefaultFolder  string `json:"default_folder"`
				QBittorrentURL string `json:"qbittorrent_url"`
			}{config.DefaultFolder, config.QBittorrentURL})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !allowedWebAPI(r) {
				webError(w, http.StatusNotFound, "Unknown dashboard endpoint.")
				return
			}
			if r.Method == http.MethodPost {
				kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if r.Header.Get("Origin") != origin || r.Header.Get("X-Torrent-Drive") != "1" || err != nil || kind != "application/json" {
					webError(w, http.StatusForbidden, "The request must come from this dashboard.")
					return
				}
				if r.ContentLength > 12<<20 {
					webError(w, http.StatusRequestEntityTooLarge, "Torrent metadata is too large.")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
			}
			select {
			case busy <- struct{}{}:
				defer func() { <-busy }()
			default:
				webError(w, http.StatusTooManyRequests, "The dashboard is busy. Try again.")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			proxy.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			webError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		if r.URL.Path == "/" && r.URL.RawPath == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == http.MethodGet {
				w.Write(page)
			}
			return
		}
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if r.Method == http.MethodGet {
				io.WriteString(w, "User-agent: *\nDisallow: /\n")
			}
			return
		}
		if r.URL.RawPath != "" || !(r.URL.Path == "/favicon.svg" || strings.HasPrefix(r.URL.Path, "/_next/static/")) {
			http.NotFound(w, r)
			return
		}
		data, err := assets.ReadFile("dist" + r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, r.URL.Path, time.Time{}, bytes.NewReader(data))
	}), nil
}

const baseUIScrollbarStyle = ".base-ui-disable-scrollbar{scrollbar-width:none}.base-ui-disable-scrollbar::-webkit-scrollbar{display:none}"

func dashboardPolicy(page []byte) string {
	policy := "default-src 'none'; script-src 'self'"
	// Next's static export includes bootstrap scripts. Permit only their exact
	// build output, without allowing arbitrary inline code or unsafe-eval.
	for _, script := range regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`).FindAllSubmatch(page, -1) {
		if len(script[1]) == 0 {
			continue
		}
		hash := sha256.Sum256(script[1])
		policy += " 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'"
	}
	// Base UI server-renders a few fixed style attributes, such as the
	// visually hidden radio inputs. Permit those exact values only.
	styles, seen := "", map[string]bool{}
	for _, attr := range regexp.MustCompile(`\sstyle="([^"]*)"`).FindAllSubmatch(page, -1) {
		value := html.UnescapeString(string(attr[1]))
		if seen[value] {
			continue
		}
		seen[value] = true
		hash := sha256.Sum256([]byte(value))
		styles += " 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'"
	}
	if styles != "" {
		styles = " 'unsafe-hashes'" + styles
	}
	// Base UI ScrollArea inserts this fixed <style> element at runtime to hide
	// native scrollbars. A test checks that the bundle still emits this text.
	hash := sha256.Sum256([]byte(baseUIScrollbarStyle))
	styles += " 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'"
	return policy + "; script-src-attr 'none'; style-src 'self'" + styles + "; img-src 'self'; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

func allowedWebAPI(r *http.Request) bool {
	if r.URL.RawPath != "" {
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/jobs" {
		return r.URL.RawQuery == ""
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/folders" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		return err == nil && len(query) == 1 && len(query["parent"]) == 1 && drive.ValidID(query.Get("parent"))
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		return false
	}
	if r.URL.Path == "/api/jobs" || r.URL.Path == "/api/jobs/clear-completed" {
		return true
	}
	parts := strings.Split(r.URL.Path, "/")
	return len(parts) == 5 && parts[1] == "api" && parts[2] == "jobs" && jobs.ValidID(parts[3]) && (parts[4] == "pause" || parts[4] == "retry" || parts[4] == "clear")
}

// Serve listens on config.Socket, or on the loopback TCP address config.Listen.
func Serve(ctx context.Context, config Config) error {
	handler, err := Handler(config)
	if err != nil {
		return err
	}
	var listener net.Listener
	if config.Socket != "" {
		if stat, err := os.Lstat(config.Socket); err == nil {
			if stat.Mode()&os.ModeSocket == 0 {
				return errors.New("dashboard socket path contains a regular file")
			}
			connection, err := net.DialTimeout("unix", config.Socket, time.Second)
			if err == nil {
				connection.Close()
				return errors.New("dashboard socket is already in use")
			}
			if err = os.Remove(config.Socket); err != nil {
				return err
			}
		}
		listener, err = net.Listen("unix", config.Socket)
		if err == nil {
			err = os.Chmod(config.Socket, 0660)
		}
	} else {
		host, _, parseErr := net.SplitHostPort(config.Listen)
		if parseErr != nil || (!net.ParseIP(host).IsLoopback() && !(config.ContainerListen && host == "0.0.0.0")) {
			return errors.New("dashboard TCP listener must use a loopback IP")
		}
		listener, err = net.Listen("tcp", config.Listen)
	}
	if err != nil {
		if listener != nil {
			listener.Close()
		}
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Print("dashboard ready")
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
