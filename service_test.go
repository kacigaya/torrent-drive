package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anacrolix/torrent/bencode"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testControlSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "td-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "control.sock")
}

func TestWorkerStateFailureStopsService(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &service{dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.serve(ctx, testControlSocket(t))
	if err == nil {
		t.Fatal("service stayed healthy after worker failure")
	}
	if ctx.Err() != nil {
		t.Fatal("service did not stop after worker failure")
	}
}

func TestStatusExcludesSessionAndMagnetSecrets(t *testing.T) {
	j := &job{ID: newID(), Magnet: "private tracker secret", Files: []fileState{{Session: "secret upload URL"}}}
	v := j.view()
	if v.ID != j.ID || v.Files != 1 {
		t.Fatal("invalid status view")
	}
}

func TestAttemptOutcomePreservesCheckpointOnClientRefresh(t *testing.T) {
	j := &job{Status: "running", Attempts: 3, Files: []fileState{{Checkpoint: 8 << 20, Uploaded: 8 << 20, Session: "private-session"}}}
	finishAttempt(j, errRecycleClient, false, false, 0)
	if j.Status != "queued" || j.Attempts != 3 || j.Error != "" || j.Files[0].Checkpoint != 8<<20 || j.Files[0].Session != "private-session" {
		t.Fatal("client refresh changed durable progress or retry budget")
	}
	finishAttempt(j, context.Canceled, false, true, 0)
	if j.Status != "paused" {
		t.Fatal("pause lost")
	}
	finishAttempt(j, context.Canceled, true, true, 0)
	if j.Status != "queued" {
		t.Fatal("shutdown did not queue work")
	}
	finishAttempt(j, errors.New("failure"), false, false, 0)
	if j.Attempts != 4 || j.Status != "retry_wait" || j.RetryAt.IsZero() {
		t.Fatal("failure retry state invalid")
	}
	finishAttempt(j, nil, false, false, 0)
	if j.Status != "complete" || j.Error != "" || !j.RetryAt.IsZero() {
		t.Fatal("completion kept stale error state")
	}
}

func TestClientRefreshWithoutProgressUsesFailureBackoff(t *testing.T) {
	j := &job{Attempts: 1, Files: []fileState{{Uploaded: 8 << 20, Checkpoint: 8 << 20}}}
	finishAttempt(j, errRecycleClient, false, false, 8<<20)
	if j.Status != "retry_wait" || j.Attempts != 2 || j.Error == "" || j.RetryAt.IsZero() {
		t.Fatal("client refresh spun without progress")
	}
}

func TestPauseDuringRefreshStaysPaused(t *testing.T) {
	j := &job{Files: []fileState{{Uploaded: 16 << 20}}, Attempts: 1}
	finishAttempt(j, errRecycleClient, false, true, 8<<20)
	if j.Status != "paused" || j.Attempts != 1 {
		t.Fatal("accepted pause was lost during refresh")
	}
}

func TestPrivateAndUnknownTorrentsDisablePublicDiscovery(t *testing.T) {
	s := &service{dir: t.TempDir()}
	j := &job{ID: newID()}
	if allow, known, err := s.discoveryPolicy(j); err != nil || allow || known {
		t.Fatal("unknown magnet enabled public discovery")
	}
	j.PublicDiscovery = true
	if allow, known, err := s.discoveryPolicy(j); err != nil || !allow || known {
		t.Fatal("explicit public discovery was ignored")
	}
	mi := testMetainfo([]byte("privacy fixture"), 16384)
	info, _ := mi.UnmarshalInfo()
	private := true
	info.Private = &private
	mi.InfoBytes, _ = bencode.Marshal(&info)
	f, err := os.Create(filepath.Join(s.dir, j.ID+".torrent"))
	if err != nil {
		t.Fatal(err)
	}
	mi.Write(f)
	f.Close()
	if allow, known, err := s.discoveryPolicy(j); err != nil || allow || !known {
		t.Fatal("private torrent enabled DHT or PEX", err)
	}
	private = false
	info.Private = &private
	mi.InfoBytes, _ = bencode.Marshal(&info)
	f, err = os.Create(filepath.Join(s.dir, j.ID+".torrent"))
	if err != nil {
		t.Fatal(err)
	}
	mi.Write(f)
	f.Close()
	if allow, known, err := s.discoveryPolicy(j); err != nil || !allow || !known {
		t.Fatal("known public torrent discovery disabled", err)
	}
}

func TestTrackerlessMagnetRequiresPublicDiscoveryOptIn(t *testing.T) {
	s := &service{dir: t.TempDir()}
	magnet := "magnet:?xt=urn:btih:0123456789012345678901234567890123456789"
	if _, err := s.submit(submitRequest{Folder: "parent-id", Magnet: magnet}); err == nil {
		t.Fatal("trackerless private-safe magnet queued without discovery")
	}
	if _, err := s.submit(submitRequest{Folder: "parent-id", Magnet: magnet, PublicDiscovery: true}); err != nil {
		t.Fatal("public magnet opt-in rejected", err)
	}
}

func TestControlledRefreshStopsAndResumesFromDriveCheckpoint(t *testing.T) {
	original := maxPeerDataPerClient
	maxPeerDataPerClient = 12 << 20
	defer func() { maxPeerDataPerClient = original }()
	runtime.GC()
	data := bytes.Repeat([]byte("x"), 20<<20)
	mi := testMetainfo(data, 1<<20)
	info, _ := mi.UnmarshalInfo()
	info.Name = "legal.txt"
	private := true
	info.Private = &private
	mi.InfoBytes, _ = bencode.Marshal(&info)
	seed := startSeeder(t, data, mi)
	host, portText, _ := net.SplitHostPort(seed.ListenAddrs()[0].String())
	var port int
	fmt.Sscan(portText, &port)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := bencode.Marshal(struct {
			Interval int `bencode:"interval"`
			Peers    []struct {
				IP   string `bencode:"ip"`
				Port int    `bencode:"port"`
			} `bencode:"peers"`
		}{30, []struct {
			IP   string `bencode:"ip"`
			Port int    `bencode:"port"`
		}{{host, port}}})
		w.Write(body)
	}))
	defer tracker.Close()
	mi.Announce = tracker.URL + "/announce"
	var metadata bytes.Buffer
	mi.Write(&metadata)
	_, drive := newFakeDrive(t)
	dir := t.TempDir()
	socket := testControlSocket(t)
	s := &service{dir: dir, drive: drive}
	j, err := s.submit(submitRequest{Folder: "parent-id", Torrent: metadata.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = s.serve(ctx, socket); !errors.Is(err, errRecycleClient) {
		t.Fatal("controlled refresh did not stop the service", err)
	}
	jobs, err := readJobs(dir)
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing saved refresh state", err)
	}
	if jobs[0].Status != "queued" || jobs[0].Attempts != 0 || jobs[0].Files[0].Checkpoint != uploadChunk || jobs[0].Files[0].Session == "" {
		b, _ := json.Marshal(jobs[0].view())
		t.Fatalf("refresh did not preserve first checkpoint: %s", b)
	}
	maxPeerDataPerClient = original
	resumeCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- (&service{dir: dir, drive: drive}).serve(resumeCtx, socket) }()
	for {
		jobs, err = readJobs(dir)
		if err != nil {
			t.Fatal(err)
		}
		if jobs[0].ID == j.ID && jobs[0].Status == "complete" {
			break
		}
		if jobs[0].Status == "retry_wait" || jobs[0].Status == "failed" {
			t.Fatal("resumed upload failed", jobs[0].Error)
		}
		if sleepContext(ctx, 20*time.Millisecond) != nil {
			t.Fatal("resumed upload timed out")
		}
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClearRemovesOnlyCompletedJobs(t *testing.T) {
	dir := t.TempDir()
	s := &service{dir: dir}
	done1, done2, queued := &job{ID: newID(), Status: "complete"}, &job{ID: newID(), Status: "complete"}, &job{ID: newID(), Status: "queued"}
	for _, j := range []*job{done1, done2, queued} {
		if err := saveJob(dir, j); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, done1.ID+".torrent"), []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	post := func(path string) int {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		return w.Code
	}
	if post("/jobs/"+queued.ID+"/clear") != 409 {
		t.Fatal("cleared an unfinished job")
	}
	if post("/jobs/"+done1.ID+"/clear") != 204 {
		t.Fatal("cannot clear a completed job")
	}
	if _, err := os.Stat(filepath.Join(dir, done1.ID+".torrent")); !os.IsNotExist(err) {
		t.Fatal("torrent metadata stayed after clear")
	}
	if post("/jobs/clear-completed") != 204 {
		t.Fatal("cannot clear completed jobs")
	}
	jobs, err := readJobs(dir)
	if err != nil || len(jobs) != 1 || jobs[0].ID != queued.ID {
		t.Fatal("clear changed the wrong jobs")
	}
}
