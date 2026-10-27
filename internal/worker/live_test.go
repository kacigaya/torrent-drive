package worker

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/kacigaya/torrent-drive/internal/drive"
	"github.com/kacigaya/torrent-drive/internal/jobs"
	"github.com/kacigaya/torrent-drive/internal/testkit"
)

// This test writes only metadata locally. Generated CC0 text remains in RAM.
func TestLiveDrive(t *testing.T) {
	if os.Getenv("TORRENT_DRIVE_LIVE") != "1" {
		t.Skip("set TORRENT_DRIVE_LIVE=1 and test credentials for a real Drive test")
	}
	if os.Getenv("TORRENT_DRIVE_VPN_DNS") == "1" {
		UseVPNDNS()
	}
	oauthPath := os.Getenv("TORRENT_DRIVE_OAUTH")
	if oauthPath == "" {
		oauthPath = "/etc/torrent-drive/oauth.json"
	}
	d, err := drive.New(oauthPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	parent := os.Getenv("TORRENT_DRIVE_TEST_PARENT")
	if parent == "" || parent == "root" || !drive.ValidID(parent) {
		t.Fatal("set TORRENT_DRIVE_TEST_PARENT to a dedicated test folder ID")
	}
	folder, err := d.GenerateID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "torrent-drive-test-" + time.Now().UTC().Format("20060102T150405")
	if err = d.EnsureFolder(ctx, folder, name, parent); err != nil {
		t.Fatal(err)
	}
	t.Logf("TEST_FOLDER https://drive.google.com/drive/folders/%s", folder)
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/run/torrent-drive/control.sock")
	}}}
	call := func(method, target string, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, "http://local"+target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("control socket unavailable")
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if r.StatusCode >= 300 {
			return nil, fmt.Errorf("control HTTP %d: %s", r.StatusCode, b)
		}
		return b, nil
	}
	data := bytes.Repeat([]byte("CC0 public domain. Torrent Drive legal transfer test.\n"), 400000)
	restartTest := os.Getenv("TORRENT_DRIVE_TEST_RESTART") == "1"
	if restartTest {
		data = append(data, data...)
	}
	for _, mode := range []string{"torrent", "magnet", "multi"} {
		t.Run(mode, func(t *testing.T) {
			content := data
			if mode != "torrent" {
				content = data[:210004]
			}
			mi := testkit.Metainfo(content, 256<<10)
			info, _ := mi.UnmarshalInfo()
			private := true
			info.Private = &private
			if mode == "multi" {
				info.Name = "multi-legal"
				info.Length = 0
				info.Files = []metainfo.FileInfo{{Path: []string{"first.txt"}, Length: 70001}, {Path: []string{"nested", "empty.txt"}, Length: 0}, {Path: []string{"nested", "résumé.txt"}, Length: 140003}}
				info.PieceLength = 64 << 10
				info.Pieces = nil
				for off := 0; off < len(content); off += 64 << 10 {
					hash := sha1.Sum(content[off:min(off+64<<10, len(content))])
					info.Pieces = append(info.Pieces, hash[:]...)
				}
			}
			mi.InfoBytes, _ = bencode.Marshal(info)
			seed := testkit.StartSeeder(t, content, mi)
			peer := seed.ListenAddrs()[0].String()
			// A loopback tracker keeps this legal fixture isolated from public swarms.
			tracker := &http.Server{ReadHeaderTimeout: 5 * time.Second}
			tracker.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host, port, _ := net.SplitHostPort(peer)
				p := 0
				fmt.Sscan(port, &p)
				body, _ := bencode.Marshal(struct {
					Interval int `bencode:"interval"`
					Peers    []struct {
						IP   string `bencode:"ip"`
						Port int    `bencode:"port"`
					} `bencode:"peers"`
				}{1, []struct {
					IP   string `bencode:"ip"`
					Port int    `bencode:"port"`
				}{{host, p}}})
				w.Write(body)
			})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			go tracker.Serve(listener)
			defer tracker.Close()
			mi.Announce = "http://" + listener.Addr().String() + "/announce"
			var metadata bytes.Buffer
			mi.Write(&metadata)
			submission := Submission{Folder: folder, Torrent: metadata.Bytes()}
			if mode == "magnet" {
				hash := mi.HashInfoBytes()
				submission.Torrent = nil
				submission.Magnet = "magnet:?xt=urn:btih:" + hash.HexString() + "&tr=" + url.QueryEscape(mi.Announce) + "&x.pe=" + url.QueryEscape(peer)
			}
			body, _ := json.Marshal(submission)
			response, err := call("POST", "/jobs", body)
			if err != nil {
				t.Fatal(err)
			}
			var submitted jobs.View
			if err = json.Unmarshal(response, &submitted); err != nil {
				t.Fatal(err)
			}
			t.Logf("JOB %s %s", mode, submitted.ID)
			restartReady := false
			for {
				response, err = call("GET", "/jobs", nil)
				if err != nil {
					if ctx.Err() != nil {
						t.Fatal(err)
					}
					time.Sleep(time.Second)
					continue
				}
				var views []jobs.View
				if err = json.Unmarshal(response, &views); err != nil {
					t.Fatal(err)
				}
				for _, j := range views {
					if j.ID != submitted.ID {
						continue
					}
					if mode == "torrent" && j.Uploaded >= drive.ChunkSize && !restartReady {
						restartReady = true
						if restartTest {
							if _, err = call("POST", "/jobs/"+j.ID+"/pause", nil); err != nil {
								t.Fatal(err)
							}
						}
						t.Log("LIVE_RESTART_READY")
					}
					if j.Status == "failed" || j.Status == "paused" && !(restartTest && mode == "torrent") {
						t.Fatalf("job stopped: %+v", j)
					}
					if j.Status != "complete" {
						continue
					}
					if j.Uploaded != int64(len(content)) || j.Verified != j.Files {
						t.Fatalf("incomplete result: %+v", j)
					}
					for _, result := range j.Results {
						var expected []byte
						if mode != "multi" {
							expected = content
						} else if strings.HasSuffix(result.Path, "first.txt") {
							expected = content[:70001]
						} else if strings.HasSuffix(result.Path, "résumé.txt") {
							expected = content[70001:]
						} else {
							expected = nil
						}
						// Drive computes sha256Checksum from the stored bytes.
						remote, exists, err := d.Get(ctx, result.ID)
						if err != nil || !exists {
							t.Fatal("Drive readback failed", err)
						}
						wanted := sha256.Sum256(expected)
						n, _ := strconv.ParseInt(remote.Size, 10, 64)
						if n != int64(len(expected)) || remote.SHA256 != hex.EncodeToString(wanted[:]) {
							t.Fatal("Drive file differs from generated source")
						}
						t.Logf("VERIFIED %s bytes=%d sha256=%s drive=https://drive.google.com/file/d/%s/view", result.Path, n, result.SHA256, result.ID)
					}
					return
				}
				select {
				case <-ctx.Done():
					t.Fatal("live test timed out")
				case <-time.After(time.Second):
				}
			}
		})
	}
}

func TestLiveQbitExport(t *testing.T) {
	if os.Getenv("TORRENT_DRIVE_LIVE") != "1" {
		t.Skip("live qBittorrent export check is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second}
	r, err := client.Get("http://127.0.0.1:8080/api/v2/torrents/info")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var jobs []struct {
		Hash string `json:"hash"`
	}
	if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&jobs) != nil || len(jobs) == 0 {
		t.Fatal("no existing qBittorrent torrent available for read-only export")
	}
	if _, err = exportQbit(ctx, jobs[0].Hash); err != nil {
		t.Fatal(err)
	}
	t.Log("Existing qBittorrent torrent exported and parsed without changing its state")
}
