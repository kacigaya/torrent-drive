package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const defaultSocket = "/run/torrent-drive/control.sock"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errRecycleClient) {
			fmt.Fprintln(os.Stderr, "Restarting from saved Drive checkpoint to release torrent client memory")
			os.Exit(75)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: torrent-drive {authorize|submit|import-qbittorrent|status|folders|pause|retry|serve|web} [options]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	socket := fs.String("socket", defaultSocket, "Local control socket")
	folder := fs.String("folder", "", "Google Drive destination folder ID")
	file := fs.String("torrent", "", "Path to a .torrent file")
	magnet := fs.String("magnet", "", "Magnet link")
	publicDiscovery := fs.Bool("public-discovery", false, "Allow public DHT and PEX for an unknown public magnet; never use for private torrents")
	id := fs.String("job", "", "Job ID")
	hash := fs.String("hash", "", "Existing qBittorrent torrent hash to copy")
	parent := fs.String("parent", "root", "List folders inside this Drive folder ID")
	watch := fs.Bool("watch", false, "Print progress every 2 seconds until the job stops")
	state := fs.String("state", "/var/lib/torrent-drive", "Private job metadata directory")
	oauth := fs.String("oauth", "/etc/torrent-drive/oauth.json", "Private OAuth credentials file")
	vpn := fs.Bool("vpn-dns", false, "Resolve through Gluetun DNS at 127.0.0.1")
	webListen := fs.String("listen", "127.0.0.1:8080", "Loopback dashboard address")
	webSocket := fs.String("http-socket", "", "Dashboard HTTP socket for Caddy")
	webOrigin := fs.String("origin", dashboardURL, "Exact browser origin allowed to access the dashboard")
	defaultFolder := fs.String("default-folder", "", "Optional default Google Drive folder ID")
	qbitURL := fs.String("qbittorrent-url", "", "Optional qBittorrent dashboard link")
	containerListen := fs.Bool("container-listen", false, "Allow binding 0.0.0.0 inside an isolated container; publish only on host loopback")
	oauthClient := fs.String("client", "", "Downloaded Google Desktop OAuth client JSON")
	authListen := fs.String("callback-listen", "127.0.0.1:53682", "OAuth callback listener; use 0.0.0.0:53682 only inside a container")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; use --help")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if args[0] == "authorize" {
		return authorize(ctx, *oauthClient, *oauth, *authListen)
	}
	if args[0] == "web" {
		runtime.GOMAXPROCS(1)
		debug.SetMemoryLimit(96 << 20)
		return serveWeb(ctx, webConfig{Origin: *webOrigin, WorkerSocket: *socket, Listen: *webListen, Socket: *webSocket, DefaultFolder: *defaultFolder, QBittorrentURL: *qbitURL, ContainerListen: *containerListen})
	}
	if args[0] == "serve" {
		runtime.GOMAXPROCS(2)
		debug.SetMemoryLimit(512 << 20)
		if *vpn {
			vpnDNS()
		}
		d, err := newDrive(*oauth)
		if err != nil {
			return err
		}
		s := &service{dir: *state, drive: d}
		return s.serve(ctx, *socket)
	}
	client := &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
	}}}
	call := func(method, target string, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, "http://local"+target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		r, err := client.Do(req)
		if err != nil {
			return nil, errors.New("cannot reach torrent-drive control socket; check the service")
		}
		defer r.Body.Close()
		b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			return nil, err
		}
		if r.StatusCode >= 300 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(b)))
		}
		return b, nil
	}
	switch args[0] {
	case "submit":
		req := submitRequest{Folder: *folder, Magnet: *magnet, PublicDiscovery: *publicDiscovery}
		if *file != "" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, maxMetadata+1))
			if err != nil {
				return err
			}
			req.Torrent = b
		}
		body, _ := json.Marshal(req)
		b, err := call("POST", "/jobs", body)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	case "import-qbittorrent":
		body, _ := json.Marshal(qbitRequest{Hash: *hash, Folder: *folder})
		b, err := call("POST", "/import-qbittorrent", body)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	case "folders":
		b, err := call("GET", "/folders?parent="+url.QueryEscape(*parent), nil)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	case "pause", "retry":
		if len(*id) != 32 || !validID(*id) {
			return errors.New("provide --job JOB_ID")
		}
		_, err := call("POST", "/jobs/"+*id+"/"+args[0], nil)
		return err
	case "status":
		unavailable := 0
		for {
			b, err := call("GET", "/jobs", nil)
			if err != nil {
				if *watch && unavailable < 20 && ctx.Err() == nil {
					unavailable++
					if sleepContext(ctx, 2*time.Second) != nil {
						return nil
					}
					continue
				}
				return err
			}
			unavailable = 0
			var views []jobView
			if json.Unmarshal(b, &views) != nil {
				return errors.New("invalid service response")
			}
			active, found := false, false
			for _, v := range views {
				if *id != "" && v.ID != *id {
					continue
				}
				found = true
				out, _ := json.Marshal(v)
				fmt.Println(string(out))
				if v.Status == "running" || v.Status == "queued" || v.Status == "retry_wait" {
					active = true
				}
			}
			if *id != "" && !found {
				return errors.New("job not found")
			}
			if !*watch || !active {
				return nil
			}
			if sleepContext(ctx, 2*time.Second) != nil {
				return nil
			}
		}
	default:
		return errors.New("unknown command; run torrent-drive without arguments for usage")
	}
}
