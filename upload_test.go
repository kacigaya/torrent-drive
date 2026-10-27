package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type bytesSource struct {
	data []byte
	read int64
}

func (s *bytesSource) ReadAt(_ context.Context, b []byte, off int64) error {
	if off < 0 || off+int64(len(b)) > int64(len(s.data)) {
		return io.ErrUnexpectedEOF
	}
	copy(b, s.data[off:])
	s.read += int64(len(b))
	return nil
}

type fakeDrive struct {
	data          []byte
	complete      bool
	lostAck       bool
	partial       bool
	expire        bool
	wrongChecksum bool
	starts        int
	puts          int
	server        *httptest.Server
}

func newFakeDrive(t *testing.T) (*fakeDrive, *driveClient) {
	t.Helper()
	f := &fakeDrive{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/generateIds"):
			json.NewEncoder(w).Encode(struct {
				IDs []string `json:"ids"`
			}{[]string{"file-id"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files/parent-id"):
			json.NewEncoder(w).Encode(driveFile{ID: "parent-id", Name: "destination", MimeType: "application/vnd.google-apps.folder"})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/files/"):
			if !f.complete {
				w.WriteHeader(404)
				return
			}
			md := md5.Sum(f.data)
			sha := sha256.Sum256(f.data)
			checksum := hex.EncodeToString(md[:])
			if f.wrongChecksum {
				checksum = strings.Repeat("0", 32)
			}
			json.NewEncoder(w).Encode(driveFile{ID: "file-id", Name: "legal.txt", Size: strconv.Itoa(len(f.data)), MD5: checksum, SHA256: hex.EncodeToString(sha[:]), Parents: []string{"parent-id"}, MimeType: "application/octet-stream"})
		case r.Method == "POST":
			f.starts++
			f.data = nil
			f.complete = false
			w.Header().Set("Location", f.server.URL+"/upload/session")
			w.WriteHeader(200)
		case r.Method == "PUT":
			rangeHeader := r.Header.Get("Content-Range")
			var total int64
			if strings.HasPrefix(rangeHeader, "bytes */") {
				total, _ = strconv.ParseInt(strings.TrimPrefix(rangeHeader, "bytes */"), 10, 64)
				if f.expire {
					f.expire = false
					f.data = nil
					w.WriteHeader(404)
					return
				}
				if total == 0 {
					f.complete = true
					w.WriteHeader(200)
					return
				}
			} else {
				var start, end int64
				fmt.Sscanf(rangeHeader, "bytes %d-%d/%d", &start, &end, &total)
				body, _ := io.ReadAll(r.Body)
				if start != int64(len(f.data)) || int64(len(body)) != end-start+1 {
					w.WriteHeader(400)
					return
				}
				if f.partial {
					body = body[:len(body)/2]
					f.partial = false
				}
				f.data = append(f.data, body...)
				f.puts++
				if f.lostAck {
					f.lostAck = false
					w.WriteHeader(503)
					return
				}
			}
			if int64(len(f.data)) == total {
				f.complete = true
				w.WriteHeader(200)
			} else {
				if len(f.data) > 0 {
					w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(f.data)-1))
				}
				w.WriteHeader(308)
			}
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.server.Close)
	d := &driveClient{http: f.server.Client(), base: f.server.URL + "/drive", upload: f.server.URL + "/upload", allowTestURL: true, pause: func(context.Context, time.Duration) error { return nil }}
	return f, d
}

func testFile(size int64) *fileState {
	return &fileState{ID: "file-id", Name: "legal.txt", Path: "legal.txt", Parent: "parent-id", Size: size}
}

func TestResumableUploadAndUncertainAcknowledgements(t *testing.T) {
	for _, mode := range []string{"normal", "lost-ack", "partial-ack", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f, d := newFakeDrive(t)
			f.lostAck = mode == "lost-ack"
			f.partial = mode == "partial-ack"
			data := bytes.Repeat([]byte("legal test data\n"), 1200000)
			if mode == "empty" {
				data = nil
			}
			source := &bytesSource{data: data}
			state := testFile(int64(len(data)))
			if err := uploadFile(context.Background(), d, source, state, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if !state.Verified || !bytes.Equal(f.data, data) {
				t.Fatal("upload changed contents or was not verified")
			}
			if f.starts != 1 {
				t.Fatalf("duplicate upload session: %d", f.starts)
			}
		})
	}
}

func TestCrashResumeReplaysOnlyUncheckpointedBytes(t *testing.T) {
	f, d := newFakeDrive(t)
	data := bytes.Repeat([]byte("legal data\n"), 1800000)
	state := testFile(int64(len(data)))
	var saved []byte
	err := uploadFile(context.Background(), d, &bytesSource{data: data}, state, func() error {
		if state.Uploaded >= 2*uploadChunk {
			return errors.New("simulated power loss before journal update")
		}
		saved, _ = json.Marshal(state)
		return nil
	})
	if err == nil {
		t.Fatal("expected simulated crash")
	}
	var resumed fileState
	if err = json.Unmarshal(saved, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint != uploadChunk {
		t.Fatalf("unexpected checkpoint: %d", resumed.Checkpoint)
	}
	source := &bytesSource{data: data}
	if err = uploadFile(context.Background(), d, source, &resumed, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !resumed.Verified || !bytes.Equal(f.data, data) {
		t.Fatal("resume corrupted file")
	}
	if source.read != int64(len(data))-uploadChunk {
		t.Fatalf("resume read %d bytes", source.read)
	}
	if f.starts != 1 {
		t.Fatal("resume created a duplicate session")
	}
}

func TestExpiredSessionRestartsSafely(t *testing.T) {
	f, d := newFakeDrive(t)
	data := bytes.Repeat([]byte("public data\n"), 800000)
	state := testFile(int64(len(data)))
	state.Session = f.server.URL + "/upload/expired"
	f.expire = true
	if err := uploadFile(context.Background(), d, &bytesSource{data: data}, state, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !state.Verified || !bytes.Equal(f.data, data) || f.starts != 1 {
		t.Fatal("expired upload was not restarted safely")
	}
}

func TestChecksumMismatchNeverSucceeds(t *testing.T) {
	f, d := newFakeDrive(t)
	f.wrongChecksum = true
	data := []byte("legal data")
	state := testFile(int64(len(data)))
	if err := uploadFile(context.Background(), d, &bytesSource{data: data}, state, func() error { return nil }); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if state.Verified {
		t.Fatal("corrupt file marked verified")
	}
}

func TestSessionURLAndRangesAreValidated(t *testing.T) {
	d := &driveClient{}
	for _, u := range []string{"http://www.googleapis.com/upload/drive/v3/files", "https://attacker.example/upload/drive/v3/files", "https://www.googleapis.com.attacker.example/upload/drive/v3/files", "https://www.googleapis.com/other", "https://x@www.googleapis.com/upload/drive/v3/files"} {
		if d.validateSession(u) {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, s := range []string{"bytes=1-10", "bytes=0-999", "bytes=0--1", "garbage"} {
		if _, err := acknowledged(driveReply{code: 308, header: http.Header{"Range": {s}}}, 100); err == nil {
			t.Fatalf("accepted range %s", s)
		}
	}
}

func TestDriveDetectedMimeTypeRemainsNormalFile(t *testing.T) {
	f := testFile(3)
	f.MD5 = "verified-digest"
	f.SHA256 = "verified-sha256"
	r := driveFile{ID: f.ID, Name: f.Name, Size: "3", MD5: f.MD5, SHA256: f.SHA256, Parents: []string{f.Parent}, MimeType: "text/plain"}
	if err := verifyFile(r, f); err != nil {
		t.Fatal("normal Drive MIME detection rejected", err)
	}
	r.MimeType = "application/vnd.google-apps.document"
	if err := verifyFile(r, f); err == nil {
		t.Fatal("Google Docs conversion accepted")
	}
}

func TestHashCheckpointContainsNoPendingPayload(t *testing.T) {
	data := bytes.Repeat([]byte("UNIQUE-PAYLOAD-BYTES-"), 20)
	data = data[:len(data)/64*64]
	f := testFile(1024)
	md, sha := md5.New(), sha256.New()
	md.Write(data)
	sha.Write(data)
	if err := checkpoint(f, int64(len(data)), md, sha); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.MD5State, data[:20]) || bytes.Contains(f.SHAState, data[:20]) {
		t.Fatal("payload persisted in hash checkpoint")
	}
	previous := f.Checkpoint
	md.Write([]byte("pending"))
	sha.Write([]byte("pending"))
	checkpoint(f, previous+7, md, sha)
	if f.Checkpoint != previous {
		t.Fatal("unaligned hash state persisted")
	}
}
