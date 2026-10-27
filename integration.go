package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

type qbitRequest struct {
	Hash   string `json:"hash"`
	Folder string `json:"folder"`
}

func exportQbit(ctx context.Context, hash string) ([]byte, error) {
	if len(hash) != 40 && len(hash) != 64 {
		return nil, errors.New("invalid qBittorrent hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
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
	b, err := io.ReadAll(io.LimitReader(r.Body, maxMetadata+1))
	if err != nil {
		return nil, err
	}
	if _, err = readMetainfo(b); err != nil {
		return nil, err
	}
	return b, nil
}

func (d *driveClient) folders(ctx context.Context, parent string) ([]byte, error) {
	if !validID(parent) {
		return nil, errors.New("invalid parent folder ID")
	}
	query := "'" + parent + "' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'"
	target := d.base + "/files?q=" + url.QueryEscape(query) + "&fields=nextPageToken,files(id,name)&pageSize=1000&supportsAllDrives=true&includeItemsFromAllDrives=true"
	r, err := d.request(ctx, "GET", target, nil, nil)
	if err != nil {
		return nil, err
	}
	if r.code != 200 {
		return nil, errorFromReply(r)
	}
	return r.body, nil
}

func (s *service) integrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /folders", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.drive.folders(r.Context(), r.URL.Query().Get("parent"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("POST /import-qbittorrent", func(w http.ResponseWriter, r *http.Request) {
		var req qbitRequest
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
		j, err := s.submit(submitRequest{Folder: req.Folder, Torrent: b})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(j.view())
	})
}
