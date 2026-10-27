package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

type submitRequest struct {
	PublicDiscovery bool   `json:"public_discovery,omitempty"`
	Folder          string `json:"folder"`
	Magnet          string `json:"magnet,omitempty"`
	Torrent         []byte `json:"torrent,omitempty"`
}

type service struct {
	dir    string
	drive  *driveClient
	mu     sync.Mutex
	active string
	cancel context.CancelFunc
}

func backoff(attempt int) time.Duration { return time.Duration(1<<min(attempt, 8)) * time.Second }

func (s *service) save(j *job) error { s.mu.Lock(); defer s.mu.Unlock(); return saveJob(s.dir, j) }

func (s *service) submit(request submitRequest) (*job, error) {
	if !validID(request.Folder) || request.Folder == "root" {
		return nil, errors.New("provide a Google Drive folder ID")
	}
	if (request.Magnet == "") == (len(request.Torrent) == 0) {
		return nil, errors.New("provide one magnet link or torrent file")
	}
	if request.Magnet != "" {
		if len(request.Magnet) > 16384 {
			return nil, errors.New("magnet link exceeds 16 KiB")
		}
		if spec, err := torrent.TorrentSpecFromMagnetUri(request.Magnet); err != nil {
			return nil, errors.New("invalid magnet link")
		} else if !hasTrackers(spec) && !request.PublicDiscovery {
			return nil, errors.New("magnet requires a tracker; use --public-discovery only for a public DHT-only magnet")
		}
	} else if _, err := readMetainfo(request.Torrent); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := readJobs(s.dir)
	if err != nil {
		return nil, err
	}
	if len(jobs) >= maxJobs {
		return nil, errors.New("queue holds 16 jobs; archive completed job metadata first")
	}
	if err = storageAvailable(s.dir, int64(len(request.Torrent))+16<<20); err != nil {
		return nil, err
	}
	j := &job{ID: newID(), Folder: request.Folder, Magnet: request.Magnet, PublicDiscovery: request.PublicDiscovery, Status: "queued", Created: time.Now().UTC()}
	if len(request.Torrent) > 0 {
		if err = atomicWrite(filepath.Join(s.dir, j.ID+".torrent"), request.Torrent); err != nil {
			return nil, err
		}
	}
	if err = saveJob(s.dir, j); err != nil {
		return nil, err
	}
	return j, nil
}

func hasTrackers(spec *torrent.TorrentSpec) bool {
	for _, tier := range spec.Trackers {
		for _, tracker := range tier {
			if strings.TrimSpace(tracker) != "" {
				return true
			}
		}
	}
	return false
}

func (s *service) handler() http.Handler {
	mux := http.NewServeMux()
	s.integrationRoutes(mux)
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		jobs, err := readJobs(s.dir)
		if err != nil {
			http.Error(w, "cannot read job metadata", 500)
			return
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].Created.Before(jobs[j].Created) })
		views := make([]jobView, 0, len(jobs))
		for _, j := range jobs {
			views = append(views, j.view())
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(views)
	})
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var req submitRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil {
			http.Error(w, "invalid submission JSON or metadata too large", 400)
			return
		}
		var trailing json.RawMessage
		if decoder.Decode(&trailing) != io.EOF {
			http.Error(w, "submission must contain one JSON object", 400)
			return
		}
		j, err := s.submit(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(j.view())
	})
	mux.HandleFunc("POST /jobs/clear-completed", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		jobs, err := readJobs(s.dir)
		if err != nil {
			http.Error(w, "cannot read job metadata", 500)
			return
		}
		for _, j := range jobs {
			if j.Status == "complete" && s.active != j.ID {
				if removeJob(s.dir, j.ID) != nil {
					http.Error(w, "cannot remove job metadata", 500)
					return
				}
			}
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /jobs/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		id, action := r.PathValue("id"), r.PathValue("action")
		s.mu.Lock()
		defer s.mu.Unlock()
		jobs, err := readJobs(s.dir)
		if err != nil {
			http.Error(w, "cannot read job metadata", 500)
			return
		}
		for _, j := range jobs {
			if j.ID != id {
				continue
			}
			if action == "pause" && s.active == id {
				s.cancel()
				w.WriteHeader(202)
				return
			}
			if s.active == id {
				http.Error(w, "job is active", 409)
				return
			}
			switch action {
			case "clear":
				if j.Status != "complete" {
					http.Error(w, "only completed jobs can be cleared", 409)
					return
				}
				if removeJob(s.dir, j.ID) != nil {
					http.Error(w, "cannot remove job metadata", 500)
					return
				}
				w.WriteHeader(204)
				return
			case "retry":
				if j.Status == "complete" {
					http.Error(w, "job already complete", 409)
					return
				}
				j.Status = "queued"
				j.Attempts = 0
				j.Error = ""
				j.RetryAt = time.Time{}
			case "pause":
				if j.Status == "complete" {
					http.Error(w, "job already complete", 409)
					return
				}
				j.Status = "paused"
			default:
				http.Error(w, "unknown job action", 404)
				return
			}
			if saveJob(s.dir, j) != nil {
				http.Error(w, "cannot save job metadata", 500)
				return
			}
			w.WriteHeader(204)
			return
		}
		http.Error(w, "job not found", 404)
	})
	return mux
}

func (s *service) worker(ctx context.Context) error {
	for ctx.Err() == nil {
		s.mu.Lock()
		jobs, err := readJobs(s.dir)
		if err != nil {
			s.mu.Unlock()
			return errors.New("cannot read job state; worker stopped")
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].Created.Before(jobs[j].Created) })
		var selected *job
		for _, j := range jobs {
			if j.Status == "queued" || j.Status == "running" || j.Status == "retry_wait" && !time.Now().Before(j.RetryAt) {
				selected = j
				break
			}
		}
		if selected == nil {
			s.mu.Unlock()
			if sleepContext(ctx, time.Second) != nil {
				return nil
			}
			continue
		}
		j := selected
		j.Status = "running"
		j.Error = ""
		s.active = j.ID
		jobCtx, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		err = saveJob(s.dir, j)
		startUploaded := j.uploadedBytes()
		s.mu.Unlock()
		if err == nil {
			err = s.runJob(jobCtx, j)
		}
		s.mu.Lock()
		interrupted := jobCtx.Err() != nil
		cancel()
		s.active = ""
		s.cancel = nil
		finishAttempt(j, err, ctx.Err() != nil, interrupted, startUploaded)
		if saveJob(s.dir, j) != nil {
			s.mu.Unlock()
			return errors.New("cannot save job result; worker stopped")
		}
		s.mu.Unlock()
		log.Printf("job %s: %s", j.ID, j.Status)
		if errors.Is(err, errRecycleClient) || errors.Is(err, errDiscoveryRestart) {
			return errRecycleClient
		}
	}
	return nil
}

func finishAttempt(j *job, err error, stopping, interrupted bool, startUploaded int64) {
	if err != nil && interrupted {
		j.Error = ""
		j.RetryAt = time.Time{}
		j.Status = "paused"
		if stopping {
			j.Status = "queued"
		}
		return
	}
	if errors.Is(err, errRecycleClient) && j.uploadedBytes() <= startUploaded {
		err = errors.New("torrent client resource budget reached without new upload progress")
	}
	j.RetryAt = time.Time{}
	switch {
	case err == nil:
		j.Status = "complete"
		j.Error = ""
	case errors.Is(err, errRecycleClient) || errors.Is(err, errDiscoveryRestart):
		j.Status = "queued"
		j.Error = ""
	case errors.Is(err, context.Canceled):
		j.Status = "paused"
		j.Error = ""
		if stopping {
			j.Status = "queued"
		}
	default:
		j.Error = err.Error()
		j.Attempts++
		j.Status = "retry_wait"
		j.RetryAt = time.Now().Add(30 * backoff(j.Attempts-1))
		if j.Attempts >= 8 {
			j.Status = "failed"
		}
	}
}

func (s *service) loadTorrent(ctx context.Context, j *job, client *torrent.Client) (*torrent.Torrent, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, j.ID+".torrent"))
	var spec *torrent.TorrentSpec
	if err == nil {
		mi, err := readMetainfo(b)
		if err != nil {
			return nil, err
		}
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			return nil, errors.New("invalid torrent specification")
		}
	} else if os.IsNotExist(err) && j.Magnet != "" {
		spec, err = torrent.TorrentSpecFromMagnetUri(j.Magnet)
		if err != nil {
			return nil, errors.New("invalid saved magnet")
		}
	} else {
		return nil, errors.New("missing torrent metadata")
	}
	// Sources and webseeds can download outside the piece scheduler. Keep them off.
	spec.Sources = nil
	spec.Webseeds = nil
	spec.DisallowDataDownload = true
	spec.DisallowDataUpload = true
	spec.DisableInitialPieceCheck = true
	t, err := addTorrent(ctx, client, spec)
	if err != nil {
		return nil, err
	}
	var metadata bytes.Buffer
	mi := t.Metainfo()
	if err = mi.Write(&metadata); err != nil {
		return nil, err
	}
	if metadata.Len() > maxMetadata {
		return nil, errors.New("magnet metadata exceeds 8 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = storageAvailable(s.dir, int64(metadata.Len())); err != nil {
		return nil, err
	}
	if err = atomicWrite(filepath.Join(s.dir, j.ID+".torrent"), metadata.Bytes()); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *service) runJob(ctx context.Context, j *job) error {
	folder, exists, err := s.drive.get(ctx, j.Folder)
	if err != nil {
		return err
	}
	if !exists || folder.Trashed || folder.MimeType != "application/vnd.google-apps.folder" {
		return errors.New("Drive destination folder is missing or invalid")
	}
	ram := newRAMStore()
	config := torrentConfig(ram)
	allowPublic, knownMetadata, err := s.discoveryPolicy(j)
	if err != nil {
		return err
	}
	config.NoDHT = !allowPublic
	config.DisablePEX = !allowPublic
	identity := sha256.Sum256([]byte(j.ID))
	// Keep the same protocol identity across controlled client refreshes.
	var peerID [20]byte
	n := copy(peerID[:], config.Bep20)
	copy(peerID[n:], identity[:])
	config.PeerID = string(peerID[:])
	client, err := torrent.NewClient(config)
	if err != nil {
		return errors.New("cannot start RAM torrent client")
	}
	defer client.Close()
	t, err := s.loadTorrent(ctx, j, client)
	if err != nil {
		return err
	}
	defer t.Drop()
	if !knownMetadata && allowPublic && privateInfo(t.Info()) {
		return errDiscoveryRestart
	}
	j.Name = t.Name()
	if len(j.Files) == 0 {
		for _, f := range t.Files() {
			if strings.Contains(f.FileInfo().Attr, "p") {
				continue
			}
			j.Files = append(j.Files, fileState{Path: f.Path(), Name: path.Base(f.Path()), Start: f.Offset(), Size: f.Length()})
		}
		if len(j.Files) == 0 {
			return errors.New("torrent contains no ordinary files")
		}
		if err = s.save(j); err != nil {
			return err
		}
	}
	if err = s.prepareFolders(ctx, j); err != nil {
		return err
	}
	source := newPieceSource(t, ram)
	t.AllowDataDownload()
	for i := range j.Files {
		f := &j.Files[i]
		if f.Verified {
			continue
		}
		if err = uploadFile(ctx, s.drive, source, f, func() error { return s.save(j) }); err != nil {
			return err
		}
	}
	for i := range j.Files {
		f := &j.Files[i]
		remote, exists, err := s.drive.get(ctx, f.ID)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("completed Drive file is missing")
		}
		if err = verifyFile(remote, f); err != nil {
			return err
		}
	}
	return nil
}

func privateInfo(info *metainfo.Info) bool { return info.Private != nil && *info.Private }

func (s *service) discoveryPolicy(j *job) (allowPublic, known bool, err error) {
	b, err := os.ReadFile(filepath.Join(s.dir, j.ID+".torrent"))
	if os.IsNotExist(err) {
		return j.PublicDiscovery, false, nil
	}
	if err != nil {
		return false, false, errors.New("cannot read torrent discovery policy")
	}
	mi, err := readMetainfo(b)
	if err != nil {
		return false, false, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return false, false, errors.New("invalid torrent discovery metadata")
	}
	return !privateInfo(&info), true, nil
}

func (s *service) prepareFolders(ctx context.Context, j *job) error {
	ids := map[string]string{".": j.Folder}
	for _, f := range j.Folders {
		ids[f.Path] = f.ID
	}
	for i := range j.Files {
		f := &j.Files[i]
		dir := path.Dir(f.Path)
		var parents []string
		for p := dir; p != "."; p = path.Dir(p) {
			parents = append(parents, p)
		}
		for k := len(parents) - 1; k >= 0; k-- {
			p := parents[k]
			if ids[p] != "" {
				continue
			}
			id, err := s.drive.generateID(ctx)
			if err != nil {
				return err
			}
			j.Folders = append(j.Folders, folderState{p, path.Base(p), id, ids[path.Dir(p)]})
			ids[p] = id
			if err = s.save(j); err != nil {
				return err
			}
		}
		f.Parent = ids[dir]
		if f.ID == "" {
			id, err := s.drive.generateID(ctx)
			if err != nil {
				return err
			}
			f.ID = id
			if err = s.save(j); err != nil {
				return err
			}
		}
	}
	for _, f := range j.Folders {
		if err := s.drive.ensureFolder(ctx, f.ID, f.Name, f.Parent); err != nil {
			return err
		}
	}
	return s.save(j)
}

func (s *service) serve(ctx context.Context, socket string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if err := requireNoSwap(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
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
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() {
		err := s.worker(ctx)
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
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return <-done
	}
	return fmt.Errorf("control socket failed")
}
