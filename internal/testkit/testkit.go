// Package testkit holds fixtures shared by tests: a local seeder and a fake
// Drive server. Only tests import it.
package testkit

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// Metainfo returns a single-file v1 torrent for data.
func Metainfo(data []byte, pieceLength int64) *metainfo.MetaInfo {
	info := metainfo.Info{Name: "legal-test.txt", PieceLength: pieceLength, Length: int64(len(data))}
	for off := int64(0); off < int64(len(data)); off += pieceLength {
		sum := sha1.Sum(data[off:min(off+pieceLength, int64(len(data)))])
		info.Pieces = append(info.Pieces, sum[:]...)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	return &metainfo.MetaInfo{InfoBytes: b}
}

type seedStore struct{ data []byte }

func (s seedStore) OpenTorrent(_ context.Context, info *metainfo.Info, _ metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{Piece: func(p metainfo.Piece) storage.PieceImpl { return seedPiece{s.data[p.Offset() : p.Offset()+p.Length()]} }, Close: func() error { return nil }}, nil
}

type seedPiece struct{ data []byte }

func (p seedPiece) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(p.data)) {
		return 0, io.EOF
	}
	n := copy(b, p.data[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}
func (p seedPiece) WriteAt([]byte, int64) (int, error) { return 0, io.ErrClosedPipe }
func (p seedPiece) MarkComplete() error                { return nil }
func (p seedPiece) MarkNotComplete() error             { return nil }
func (p seedPiece) Completion() storage.Completion {
	return storage.Completion{Ok: true, Complete: true}
}

// StartSeeder seeds data on a loopback-only client. Add it with AddClientPeer.
func StartSeeder(t *testing.T, data []byte, mi *metainfo.MetaInfo) *torrent.Client {
	t.Helper()
	config := torrent.NewDefaultClientConfig()
	config.DefaultStorage = seedStore{data}
	config.NoDHT = true
	config.DisableTrackers = true
	config.NoDefaultPortForwarding = true
	config.DisableUTP = true
	config.DisableWebtorrent = true
	config.DisableWebseeds = true
	config.DisableIPv6 = true
	config.ListenHost = func(string) string { return "127.0.0.1" }
	config.ListenPort = 0
	config.Seed = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatal(err)
	}
	if spec.InfoHash.IsZero() {
		// Pure v2 torrents still need a short locator in this library version.
		spec.InfoHash = *spec.InfoHashV2.Value.ToShort()
		spec.InfoHashV2.SetNone()
	}
	if _, _, err = c.AddTorrentSpec(spec); err != nil {
		t.Fatal(err)
	}
	return c
}

// FakeDrive serves the Drive endpoints one upload uses. It stores one file,
// "file-id", in parent "parent-id". Set the flags to inject failures.
type FakeDrive struct {
	Data          []byte
	Complete      bool
	LostAck       bool // keep the next chunk but answer 503
	Partial       bool // keep only half of the next chunk
	Expire        bool // answer 404 to the next cursor query
	WrongChecksum bool
	Starts        int
	Puts          int
	Server        *httptest.Server
}

// NewFakeDrive starts a fake Drive server. Pass its URL to drive.NewForTest.
func NewFakeDrive(t *testing.T) *FakeDrive {
	t.Helper()
	f := &FakeDrive{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/generateIds"):
			json.NewEncoder(w).Encode(struct {
				IDs []string `json:"ids"`
			}{[]string{"file-id"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files/parent-id"):
			json.NewEncoder(w).Encode(map[string]any{"id": "parent-id", "name": "destination", "mimeType": "application/vnd.google-apps.folder"})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/files/"):
			if !f.Complete {
				w.WriteHeader(404)
				return
			}
			md := md5.Sum(f.Data)
			sha := sha256.Sum256(f.Data)
			checksum := hex.EncodeToString(md[:])
			if f.WrongChecksum {
				checksum = strings.Repeat("0", 32)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "file-id", "name": "legal.txt", "size": strconv.Itoa(len(f.Data)), "md5Checksum": checksum, "sha256Checksum": hex.EncodeToString(sha[:]), "parents": []string{"parent-id"}, "mimeType": "application/octet-stream"})
		case r.Method == "POST":
			f.Starts++
			f.Data = nil
			f.Complete = false
			w.Header().Set("Location", f.Server.URL+"/upload/session")
			w.WriteHeader(200)
		case r.Method == "PUT":
			rangeHeader := r.Header.Get("Content-Range")
			var total int64
			if strings.HasPrefix(rangeHeader, "bytes */") {
				total, _ = strconv.ParseInt(strings.TrimPrefix(rangeHeader, "bytes */"), 10, 64)
				if f.Expire {
					f.Expire = false
					f.Data = nil
					w.WriteHeader(404)
					return
				}
				if total == 0 {
					f.Complete = true
					w.WriteHeader(200)
					return
				}
			} else {
				var start, end int64
				fmt.Sscanf(rangeHeader, "bytes %d-%d/%d", &start, &end, &total)
				body, _ := io.ReadAll(r.Body)
				if start != int64(len(f.Data)) || int64(len(body)) != end-start+1 {
					w.WriteHeader(400)
					return
				}
				if f.Partial {
					body = body[:len(body)/2]
					f.Partial = false
				}
				f.Data = append(f.Data, body...)
				f.Puts++
				if f.LostAck {
					f.LostAck = false
					w.WriteHeader(503)
					return
				}
			}
			if int64(len(f.Data)) == total {
				f.Complete = true
				w.WriteHeader(200)
			} else {
				if len(f.Data) > 0 {
					w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(f.Data)-1))
				}
				w.WriteHeader(308)
			}
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.Server.Close)
	return f
}
