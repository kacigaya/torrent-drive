// Command torrent-drive downloads torrents into bounded RAM and uploads the
// original files to Google Drive.
//
// "serve" runs the worker, "web" runs the dashboard, and "authorize" saves
// Google OAuth credentials. The other commands talk to the worker socket.
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

	"github.com/kacigaya/torrent-drive/internal/auth"
	"github.com/kacigaya/torrent-drive/internal/drive"
	"github.com/kacigaya/torrent-drive/internal/jobs"
	"github.com/kacigaya/torrent-drive/internal/swarm"
	"github.com/kacigaya/torrent-drive/internal/web"
	"github.com/kacigaya/torrent-drive/internal/worker"
)

const usage = "usage: torrent-drive {authorize|submit|import-qbittorrent|status|folders|pause|retry|serve|web} [options]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Exit 75 (temporary failure) tells the supervisor to restart the worker.
		if errors.Is(err, swarm.ErrRecycleClient) {
			fmt.Fprintln(os.Stderr, "Restarting from saved Drive checkpoint to release torrent client memory")
			os.Exit(75)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	socket := fs.String("socket", "/run/torrent-drive/control.sock", "Local control socket")
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
	webOrigin := fs.String("origin", web.DefaultOrigin, "Exact browser origin allowed to access the dashboard")
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

	switch args[0] {
	case "authorize":
		return auth.Authorize(ctx, *oauthClient, *oauth, *authListen)
	case "web":
		runtime.GOMAXPROCS(1)
		debug.SetMemoryLimit(96 << 20)
		return web.Serve(ctx, web.Config{Origin: *webOrigin, WorkerSocket: *socket, Listen: *webListen, Socket: *webSocket, DefaultFolder: *defaultFolder, QBittorrentURL: *qbitURL, ContainerListen: *containerListen})
	case "serve":
		runtime.GOMAXPROCS(2)
		debug.SetMemoryLimit(512 << 20)
		if *vpn {
			worker.UseVPNDNS()
		}
		d, err := drive.New(*oauth)
		if err != nil {
			return err
		}
		return worker.New(*state, d).Serve(ctx, *socket)
	}

	c := newControlClient(*socket)
	switch args[0] {
	case "submit":
		req := worker.Submission{Folder: *folder, Magnet: *magnet, PublicDiscovery: *publicDiscovery}
		if *file != "" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			if req.Torrent, err = io.ReadAll(io.LimitReader(f, swarm.MaxMetadata+1)); err != nil {
				return err
			}
		}
		return c.print(ctx, "POST", "/jobs", req)
	case "import-qbittorrent":
		return c.print(ctx, "POST", "/import-qbittorrent", worker.QbitImport{Hash: *hash, Folder: *folder})
	case "folders":
		return c.print(ctx, "GET", "/folders?parent="+url.QueryEscape(*parent), nil)
	case "pause", "retry":
		if !jobs.ValidID(*id) {
			return errors.New("provide --job JOB_ID")
		}
		_, err := c.call(ctx, "POST", "/jobs/"+*id+"/"+args[0], nil)
		return err
	case "status":
		return status(ctx, c, *id, *watch)
	default:
		return errors.New("unknown command; run torrent-drive without arguments for usage")
	}
}

// status prints one JSON line per job. With watch, it repeats every 2 seconds
// while a job is active and waits through short worker restarts.
func status(ctx context.Context, c controlClient, id string, watch bool) error {
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
			return true
		}
	}
	unavailable := 0
	for {
		b, err := c.call(ctx, "GET", "/jobs", nil)
		if err != nil {
			if watch && unavailable < 20 && ctx.Err() == nil {
				unavailable++
				if !wait() {
					return nil
				}
				continue
			}
			return err
		}
		unavailable = 0
		var views []jobs.View
		if json.Unmarshal(b, &views) != nil {
			return errors.New("invalid service response")
		}
		active, found := false, false
		for _, v := range views {
			if id != "" && v.ID != id {
				continue
			}
			found = true
			out, _ := json.Marshal(v)
			fmt.Println(string(out))
			active = active || v.Active()
		}
		if id != "" && !found {
			return errors.New("job not found")
		}
		if !watch || !active || !wait() {
			return nil
		}
	}
}

// controlClient calls the worker API over its Unix socket.
type controlClient struct{ http *http.Client }

func newControlClient(socket string) controlClient {
	return controlClient{&http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}

func (c controlClient) call(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://local"+target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("cannot reach torrent-drive control socket; check the service")
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if r.StatusCode >= 300 {
		return nil, errors.New(strings.TrimSpace(string(b)))
	}
	return b, nil
}

// print sends v as JSON, or no body when v is nil, and prints the reply.
func (c controlClient) print(ctx context.Context, method, target string, v any) error {
	var body []byte
	if v != nil {
		body, _ = json.Marshal(v)
	}
	b, err := c.call(ctx, method, target, body)
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	return nil
}
