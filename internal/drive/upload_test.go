package drive

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/kacigaya/torrent-drive/internal/testkit"
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

func newFakeDrive(t *testing.T) (*testkit.FakeDrive, *Client) {
	f := testkit.NewFakeDrive(t)
	return f, NewForTest(f.Server.URL, f.Server.Client())
}

func testFile(size int64) *Transfer {
	return &Transfer{ID: "file-id", Name: "legal.txt", Path: "legal.txt", Parent: "parent-id", Size: size}
}

func TestResumableUploadAndUncertainAcknowledgements(t *testing.T) {
	for _, mode := range []string{"normal", "lost-ack", "partial-ack", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f, d := newFakeDrive(t)
			f.LostAck = mode == "lost-ack"
			f.Partial = mode == "partial-ack"
			data := bytes.Repeat([]byte("legal test data\n"), 1200000)
			if mode == "empty" {
				data = nil
			}
			source := &bytesSource{data: data}
			state := testFile(int64(len(data)))
			if err := d.Upload(context.Background(), source, state, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if !state.Verified || !bytes.Equal(f.Data, data) {
				t.Fatal("upload changed contents or was not verified")
			}
			if f.Starts != 1 {
				t.Fatalf("duplicate upload session: %d", f.Starts)
			}
		})
	}
}

func TestCrashResumeReplaysOnlyUncheckpointedBytes(t *testing.T) {
	f, d := newFakeDrive(t)
	data := bytes.Repeat([]byte("legal data\n"), 1800000)
	state := testFile(int64(len(data)))
	var saved []byte
	err := d.Upload(context.Background(), &bytesSource{data: data}, state, func() error {
		if state.Uploaded >= 2*ChunkSize {
			return errors.New("simulated power loss before journal update")
		}
		saved, _ = json.Marshal(state)
		return nil
	})
	if err == nil {
		t.Fatal("expected simulated crash")
	}
	var resumed Transfer
	if err = json.Unmarshal(saved, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint != ChunkSize {
		t.Fatalf("unexpected checkpoint: %d", resumed.Checkpoint)
	}
	source := &bytesSource{data: data}
	if err = d.Upload(context.Background(), source, &resumed, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !resumed.Verified || !bytes.Equal(f.Data, data) {
		t.Fatal("resume corrupted file")
	}
	if source.read != int64(len(data))-ChunkSize {
		t.Fatalf("resume read %d bytes", source.read)
	}
	if f.Starts != 1 {
		t.Fatal("resume created a duplicate session")
	}
}

func TestExpiredSessionRestartsSafely(t *testing.T) {
	f, d := newFakeDrive(t)
	data := bytes.Repeat([]byte("public data\n"), 800000)
	state := testFile(int64(len(data)))
	state.Session = f.Server.URL + "/upload/expired"
	f.Expire = true
	if err := d.Upload(context.Background(), &bytesSource{data: data}, state, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !state.Verified || !bytes.Equal(f.Data, data) || f.Starts != 1 {
		t.Fatal("expired upload was not restarted safely")
	}
}

func TestChecksumMismatchNeverSucceeds(t *testing.T) {
	f, d := newFakeDrive(t)
	f.WrongChecksum = true
	data := []byte("legal data")
	state := testFile(int64(len(data)))
	if err := d.Upload(context.Background(), &bytesSource{data: data}, state, func() error { return nil }); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if state.Verified {
		t.Fatal("corrupt file marked verified")
	}
}

func TestSessionURLAndRangesAreValidated(t *testing.T) {
	d := &Client{}
	for _, u := range []string{"http://www.googleapis.com/upload/drive/v3/files", "https://attacker.example/upload/drive/v3/files", "https://www.googleapis.com.attacker.example/upload/drive/v3/files", "https://www.googleapis.com/other", "https://x@www.googleapis.com/upload/drive/v3/files"} {
		if d.validSession(u) {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, s := range []string{"bytes=1-10", "bytes=0-999", "bytes=0--1", "garbage"} {
		if _, err := acknowledged(reply{code: 308, header: http.Header{"Range": {s}}}, 100); err == nil {
			t.Fatalf("accepted range %s", s)
		}
	}
}

func TestDriveDetectedMimeTypeRemainsNormalFile(t *testing.T) {
	f := testFile(3)
	f.MD5 = "verified-digest"
	f.SHA256 = "verified-sha256"
	r := File{ID: f.ID, Name: f.Name, Size: "3", MD5: f.MD5, SHA256: f.SHA256, Parents: []string{f.Parent}, MimeType: "text/plain"}
	if err := matches(r, f); err != nil {
		t.Fatal("normal Drive MIME detection rejected", err)
	}
	r.MimeType = "application/vnd.google-apps.document"
	if err := matches(r, f); err == nil {
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
