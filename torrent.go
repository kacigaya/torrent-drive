package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

const maxMetadata = 8 << 20

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && utf8.ValidString(s) && !strings.ContainsAny(s, "/\\\x00") && len(s) <= 1024
}

func validateInfo(info *metainfo.Info) error {
	if !validName(info.BestName()) {
		return errors.New("torrent has an unsafe or invalid name")
	}
	if info.PieceLength <= 0 || info.PieceLength > maxPiece {
		return errors.New("torrent piece length must be at most 64 MiB")
	}
	var total int64
	files := info.UpvertedFiles()
	for _, f := range files {
		if f.Length < 0 || f.Length > 256<<30-total {
			return errors.New("torrent total size must be at most 256 GiB")
		}
		total += f.Length
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
	if len(files) > 4096 {
		return errors.New("torrent exceeds 4096 files")
	}
	seen := make(map[string]bool)
	for _, f := range files {
		if f.Length < 0 {
			return errors.New("negative file length")
		}
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

func torrentConfig(s *ramStore) *torrent.ClientConfig {
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
	c.MaxUnverifiedBytes = maxPiece
	c.PieceHashersPerTorrent = 1
	c.DownloadRateLimiter = rate.NewLimiter(16<<20, 64<<10)
	c.UploadRateLimiter = rate.NewLimiter(64<<10, 64<<10)
	return c
}

func readMetainfo(data []byte) (*metainfo.MetaInfo, error) {
	if len(data) > maxMetadata {
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

func addTorrent(ctx context.Context, client *torrent.Client, spec *torrent.TorrentSpec) (*torrent.Torrent, error) {
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
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Drop()
		return nil, ctx.Err()
	case <-timer.C:
		t.Drop()
		return nil, errors.New("magnet metadata did not arrive within 5 minutes")
	case <-t.GotInfo():
	}
	if err = validateInfo(t.Info()); err != nil {
		t.Drop()
		return nil, err
	}
	metadata := t.Metainfo().InfoBytes
	if !expectedV1.IsZero() && sha1.Sum(metadata) != expectedV1 {
		t.Drop()
		return nil, errors.New("torrent v1 metadata hash does not match")
	}
	if expectedV2.Ok && sha256.Sum256(metadata) != expectedV2.Value {
		t.Drop()
		return nil, errors.New("torrent v2 metadata hash does not match")
	}
	return t, nil
}

type pieceSource struct {
	t             *torrent.Torrent
	store         *ramStore
	current       int
	writeErrors   chan error
	peerDataLimit int64
}

var errRecycleClient = errors.New("refresh torrent client at saved Drive checkpoint")
var errDiscoveryRestart = errors.New("restart with private torrent discovery disabled")

var maxPeerDataPerClient int64 = 4 << 30

func newPieceSource(t *torrent.Torrent, s *ramStore) *pieceSource {
	p := &pieceSource{t: t, store: s, current: -1, writeErrors: make(chan error, 1), peerDataLimit: maxPeerDataPerClient}
	t.SetOnWriteChunkError(p.onWriteError)
	return p
}

func (p *pieceSource) checkBudget() error {
	stats := p.t.Stats()
	if stats.BytesReadData.Int64() >= p.peerDataLimit {
		return errRecycleClient
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if memory.HeapAlloc >= 256<<20 {
		return errRecycleClient
	}
	return nil
}

func (p *pieceSource) onWriteError(err error) {
	// Cancelled requests can still deliver late blocks. Storage rejects them;
	// the library re-pends them while their piece priority stays None.
	if errors.Is(err, errRetiredPiece) {
		return
	}
	select {
	case p.writeErrors <- err:
	default:
	}
}

func (p *pieceSource) selectPiece(ctx context.Context, index int) error {
	if err := p.checkBudget(); err != nil {
		return err
	}
	if p.current == index {
		return nil
	}
	if p.current >= 0 {
		p.t.Piece(p.current).SetPriority(torrent.PiecePriorityNone)
		p.store.release()
		p.t.Piece(p.current).UpdateCompletion()
	}
	if err := p.store.begin(index, int(p.t.Info().Piece(index).Length())); err != nil {
		return err
	}
	p.current = index
	p.t.Piece(index).UpdateCompletion()
	p.t.Piece(index).SetPriority(torrent.PiecePriorityHigh)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	for {
		if p.t.Piece(index).State().Complete {
			return nil
		}
		if err := p.checkBudget(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("piece stalled for 10 minutes; job can resume")
		case err := <-p.writeErrors:
			return fmt.Errorf("torrent storage rejected a write: %w", err)
		case <-ticker.C:
		}
	}
}

func (p *pieceSource) ReadAt(ctx context.Context, b []byte, off int64) error {
	last := p.t.Info().Piece(p.t.NumPieces() - 1)
	extent := last.Offset() + last.Length()
	if off < 0 || off > extent || int64(len(b)) > extent-off {
		return io.ErrUnexpectedEOF
	}
	for len(b) > 0 {
		index := int(off / p.t.Info().PieceLength)
		if err := p.selectPiece(ctx, index); err != nil {
			return err
		}
		within := off - int64(index)*p.t.Info().PieceLength
		n := min(int64(len(b)), p.t.Info().Piece(index).Length()-within)
		if err := p.store.copyVerified(index, b[:n], within); err != nil {
			return err
		}
		b, off = b[n:], off+n
	}
	return nil
}

func vpnDNS() {
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", "127.0.0.1:53")
	}}
}

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
	return fmt.Errorf("swap is enabled; disable swap or use a cgroup with memory.swap.max=0")
}
