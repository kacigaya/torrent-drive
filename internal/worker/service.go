// Package worker runs queued jobs one at a time and serves the control API
// on a local Unix socket.
package worker

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/kacigaya/torrent-drive/internal/drive"
	"github.com/kacigaya/torrent-drive/internal/jobs"
	"github.com/kacigaya/torrent-drive/internal/swarm"
)

// Submission adds one job. Give exactly one of Magnet or Torrent.
type Submission struct {
	PublicDiscovery bool   `json:"public_discovery,omitempty"`
	Folder          string `json:"folder"`
	Magnet          string `json:"magnet,omitempty"`
	Torrent         []byte `json:"torrent,omitempty"`
}

// QbitImport copies an existing qBittorrent torrent into a new job.
type QbitImport struct {
	Hash   string `json:"hash"`
	Folder string `json:"folder"`
}

type Service struct {
	store jobs.Store
	drive *drive.Client
	// mu guards the store and the active job fields.
	mu     sync.Mutex
	active string
	cancel context.CancelFunc
}

func New(stateDir string, d *drive.Client) *Service {
	return &Service{store: jobs.Store{Dir: stateDir}, drive: d}
}

func (s *Service) save(j *jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Save(j)
}

func (s *Service) Submit(req Submission) (*jobs.Job, error) {
	if !drive.ValidID(req.Folder) || req.Folder == "root" {
		return nil, errors.New("provide a Google Drive folder ID")
	}
	if (req.Magnet == "") == (len(req.Torrent) == 0) {
		return nil, errors.New("provide one magnet link or torrent file")
	}
	if req.Magnet != "" {
		if len(req.Magnet) > 16384 {
			return nil, errors.New("magnet link exceeds 16 KiB")
		}
		spec, err := torrent.TorrentSpecFromMagnetUri(req.Magnet)
		if err != nil {
			return nil, errors.New("invalid magnet link")
		}
		if !swarm.HasTrackers(spec) && !req.PublicDiscovery {
			return nil, errors.New("magnet requires a tracker; use --public-discovery only for a public DHT-only magnet")
		}
	} else if _, err := swarm.ReadMetainfo(req.Torrent); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List()
	if err != nil {
		return nil, err
	}
	if len(list) >= jobs.MaxJobs {
		return nil, errors.New("queue holds 16 jobs; archive completed job metadata first")
	}
	// Reserve room for the manifest to grow as files and checkpoints are added.
	if err = s.store.CheckSpace(int64(len(req.Torrent)) + 16<<20); err != nil {
		return nil, err
	}
	j := &jobs.Job{ID: jobs.NewID(), Folder: req.Folder, Magnet: req.Magnet, PublicDiscovery: req.PublicDiscovery, Status: jobs.Queued, Created: time.Now().UTC()}
	if len(req.Torrent) > 0 {
		if err = s.store.SaveTorrent(j.ID, req.Torrent); err != nil {
			return nil, err
		}
	}
	if err = s.store.Save(j); err != nil {
		return nil, err
	}
	return j, nil
}

// Serve runs the worker and the control socket until ctx ends or the worker
// stops. It returns swarm.ErrRecycleClient when the process should restart.
func (s *Service) Serve(ctx context.Context, socket string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if err := requireNoSwap(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.store.Dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.store.Dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another worker holds this state directory")
	}
	if _, err = os.Lstat(socket); err == nil {
		if err = os.Remove(socket); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err = os.Chmod(socket, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() {
		err := s.loop(ctx)
		done <- err
		if err != nil {
			stop()
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Print("control socket ready; torrent payload storage is RAM only")
	if err = server.Serve(listener); errors.Is(err, http.ErrServerClosed) {
		return <-done
	}
	return errors.New("control socket failed")
}

// UseVPNDNS sends all name lookups to the DNS server of a VPN namespace,
// such as Gluetun on 127.0.0.1:53.
func UseVPNDNS() {
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", "127.0.0.1:53")
	}}
}

// requireNoSwap refuses to start if payload pages could reach a swap device.
func requireNoSwap() error {
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return err
	}
	if len(strings.Fields(string(b))) <= 5 {
		return nil
	}
	cg, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(cg), "\n") {
		if strings.HasPrefix(line, "0::/") {
			v, err := os.ReadFile("/sys/fs/cgroup/" + strings.TrimPrefix(line, "0::/") + "/memory.swap.max")
			if err == nil && strings.TrimSpace(string(v)) == "0" {
				return nil
			}
		}
	}
	return errors.New("swap is enabled; disable swap or use a cgroup with memory.swap.max=0")
}
