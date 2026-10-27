package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/kacigaya/torrent-drive/internal/jobs"
	"github.com/kacigaya/torrent-drive/internal/swarm"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// Handler serves the control API. It trusts its caller: only the CLI and the
// dashboard gateway can reach the Unix socket.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs", s.listJobs)
	mux.HandleFunc("POST /jobs", s.submitJob)
	mux.HandleFunc("POST /jobs/clear-completed", s.clearCompleted)
	mux.HandleFunc("POST /jobs/{id}/{action}", s.jobAction)
	mux.HandleFunc("GET /folders", s.listFolders)
	mux.HandleFunc("POST /import-qbittorrent", s.importQbit)
	return mux
}

func (s *Service) listJobs(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	list, err := s.store.List()
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "cannot read job metadata", 500)
		return
	}
	views := make([]jobs.View, 0, len(list))
	for _, j := range list {
		views = append(views, j.View())
	}
	writeJSON(w, 200, views)
}

func (s *Service) submitJob(w http.ResponseWriter, r *http.Request) {
	var req Submission
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
	j, err := s.Submit(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 201, j.View())
}

func (s *Service) clearCompleted(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List()
	if err != nil {
		http.Error(w, "cannot read job metadata", 500)
		return
	}
	for _, j := range list {
		if j.Status == jobs.Complete && s.active != j.ID {
			if s.store.Remove(j.ID) != nil {
				http.Error(w, "cannot remove job metadata", 500)
				return
			}
		}
	}
	w.WriteHeader(204)
}

// jobAction handles pause, retry, and clear for one job.
func (s *Service) jobAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.store.Find(id)
	if err != nil {
		http.Error(w, "cannot read job metadata", 500)
		return
	}
	if j == nil {
		http.Error(w, "job not found", 404)
		return
	}
	if s.active == id {
		if action != "pause" {
			http.Error(w, "job is active", 409)
			return
		}
		// The run loop sees the cancelled context and saves the job as paused.
		s.cancel()
		w.WriteHeader(202)
		return
	}
	switch action {
	case "clear":
		if j.Status != jobs.Complete {
			http.Error(w, "only completed jobs can be cleared", 409)
			return
		}
		if s.store.Remove(j.ID) != nil {
			http.Error(w, "cannot remove job metadata", 500)
			return
		}
		w.WriteHeader(204)
		return
	case "retry", "pause":
		if j.Status == jobs.Complete {
			http.Error(w, "job already complete", 409)
			return
		}
		if action == "pause" {
			j.Status = jobs.Paused
		} else {
			j.Status, j.Attempts, j.Error, j.RetryAt = jobs.Queued, 0, "", time.Time{}
		}
	default:
		http.Error(w, "unknown job action", 404)
		return
	}
	if s.store.Save(j) != nil {
		http.Error(w, "cannot save job metadata", 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Service) listFolders(w http.ResponseWriter, r *http.Request) {
	b, err := s.drive.Folders(r.Context(), r.URL.Query().Get("parent"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (s *Service) importQbit(w http.ResponseWriter, r *http.Request) {
	var req QbitImport
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		http.Error(w, "invalid import request", 400)
		return
	}
	b, err := exportQbit(r.Context(), req.Hash)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	j, err := s.Submit(Submission{Folder: req.Folder, Torrent: b})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 201, j.View())
}

// exportQbit reads a .torrent from qBittorrent's Web UI. It expects qBittorrent
// on 127.0.0.1:8080 in the same network namespace, with local auth bypass.
func exportQbit(ctx context.Context, hash string) ([]byte, error) {
	if _, err := hex.DecodeString(hash); err != nil || len(hash) != 40 && len(hash) != 64 {
		return nil, errors.New("invalid qBittorrent hash")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8080/api/v2/torrents/export?hash="+hash, nil)
	if err != nil {
		return nil, err
	}
	r, err := client.Do(req)
	if err != nil {
		return nil, errors.New("cannot reach qBittorrent inside VPN namespace")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, errors.New("qBittorrent export failed; export the .torrent file in qBittorrent and submit it")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, swarm.MaxMetadata+1))
	if err != nil {
		return nil, err
	}
	if _, err = swarm.ReadMetainfo(b); err != nil {
		return nil, err
	}
	return b, nil
}
