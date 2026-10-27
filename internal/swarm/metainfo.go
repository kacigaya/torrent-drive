// Package swarm downloads verified torrent pieces into a bounded RAM window.
package swarm

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// MaxMetadata is the largest accepted .torrent file.
const MaxMetadata = 8 << 20

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && utf8.ValidString(s) && !strings.ContainsAny(s, "/\\\x00") && len(s) <= 1024
}

// validateInfo enforces size limits and safe, unique file paths.
func validateInfo(info *metainfo.Info) error {
	if !validName(info.BestName()) {
		return errors.New("torrent has an unsafe or invalid name")
	}
	if info.PieceLength <= 0 || info.PieceLength > MaxPiece {
		return errors.New("torrent piece length must be at most 64 MiB")
	}
	files := info.UpvertedFiles()
	if len(files) > 4096 {
		return errors.New("torrent exceeds 4096 files")
	}
	var total int64
	seen := make(map[string]bool)
	for _, f := range files {
		if f.Length < 0 || f.Length > 256<<30-total {
			return errors.New("torrent total size must be at most 256 GiB")
		}
		total += f.Length
		if strings.Contains(f.Attr, "l") {
			return errors.New("symlink torrents are not supported")
		}
		for _, part := range f.BestPath() {
			if !validName(part) {
				return errors.New("unsafe torrent path")
			}
		}
		path := strings.Join(f.BestPath(), "/")
		if seen[path] {
			return errors.New("duplicate torrent file path")
		}
		seen[path] = true
	}
	if total == 0 {
		return errors.New("torrent must contain at least one byte")
	}
	if info.HasV1() && (len(info.Pieces)%20 != 0 || !info.HasV2() && int64(len(info.Pieces)/20) != (total+info.PieceLength-1)/info.PieceLength) {
		return errors.New("torrent piece hashes do not match its size")
	}
	if info.NumPieces() > 262144 {
		return errors.New("torrent exceeds 262144 pieces")
	}
	for name := range seen {
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")] {
				return errors.New("torrent file conflicts with a directory path")
			}
		}
	}
	return nil
}

// ReadMetainfo parses a .torrent file and applies the service limits.
func ReadMetainfo(data []byte) (*metainfo.MetaInfo, error) {
	if len(data) > MaxMetadata {
		return nil, errors.New("torrent metadata exceeds 8 MiB")
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid torrent metadata")
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, errors.New("invalid torrent info")
	}
	if err = validateInfo(&info); err != nil {
		return nil, err
	}
	return mi, nil
}

// Private reports whether a torrent forbids DHT and PEX.
func Private(info *metainfo.Info) bool { return info.Private != nil && *info.Private }

// HasTrackers reports whether a spec names at least one tracker.
func HasTrackers(spec *torrent.TorrentSpec) bool {
	for _, tier := range spec.Trackers {
		for _, tracker := range tier {
			if strings.TrimSpace(tracker) != "" {
				return true
			}
		}
	}
	return false
}

// NewClientConfig returns a download-only client config that stores pieces in s.
// Public discovery is off. Callers enable DHT and PEX only for public torrents.
func NewClientConfig(s *Store) *torrent.ClientConfig {
	c := torrent.NewDefaultClientConfig()
	c.DefaultStorage = s
	c.NoDHT = true
	c.DisablePEX = true
	// Library errors can include private tracker URLs. Report only our job errors.
	c.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	c.NoUpload = true
	c.Seed = false
	c.ListenPort = 0
	c.NoDefaultPortForwarding = true
	c.AcceptPeerConnections = false
	c.DisableUTP = true
	c.DisableWebtorrent = true
	c.DisableWebseeds = true
	c.DisableIPv6 = true
	c.EstablishedConnsPerTorrent = 32
	c.HalfOpenConnsPerTorrent = 4
	c.TotalHalfOpenConns = 4
	c.TorrentPeersHighWater = 128
	c.TorrentPeersLowWater = 32
	c.MaxUnverifiedBytes = MaxPiece
	c.PieceHashersPerTorrent = 1
	c.DownloadRateLimiter = rate.NewLimiter(16<<20, 64<<10)
	c.UploadRateLimiter = rate.NewLimiter(64<<10, 64<<10)
	return c
}

// Add adds spec, waits up to 5 minutes for metadata, and checks it against
// the requested v1 and v2 info hashes.
func Add(ctx context.Context, client *torrent.Client, spec *torrent.TorrentSpec) (*torrent.Torrent, error) {
	expectedV1 := spec.InfoHash
	expectedV2 := spec.InfoHashV2
	if spec.InfoHash.IsZero() {
		if !expectedV2.Ok {
			return nil, errors.New("torrent has no valid info hash")
		}
		// v1.61 requires a nonzero short locator even for a pure v2 torrent.
		// Let metadata resolve it, then check the original full SHA-256 hash.
		copySpec := *spec
		copySpec.InfoHash = *expectedV2.Value.ToShort()
		copySpec.InfoHashV2.SetNone()
		spec = &copySpec
	}
	t, _, err := client.AddTorrentSpec(spec)
	if err != nil {
		return nil, errors.New("cannot add torrent to RAM client")
	}
	fail := func(err error) (*torrent.Torrent, error) { t.Drop(); return nil, err }
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		return fail(errors.New("magnet metadata did not arrive within 5 minutes"))
	case <-t.GotInfo():
	}
	if err = validateInfo(t.Info()); err != nil {
		return fail(err)
	}
	metadata := t.Metainfo().InfoBytes
	if !expectedV1.IsZero() && sha1.Sum(metadata) != expectedV1 {
		return fail(errors.New("torrent v1 metadata hash does not match"))
	}
	if expectedV2.Ok && sha256.Sum256(metadata) != expectedV2.Value {
		return fail(errors.New("torrent v2 metadata hash does not match"))
	}
	return t, nil
}
