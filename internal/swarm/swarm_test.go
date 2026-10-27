package swarm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/kacigaya/torrent-drive/internal/testkit"
)

func TestRAMWindowAndVerificationGate(t *testing.T) {
	s := NewStore()
	if err := s.begin(0, 32768); err != nil {
		t.Fatal(err)
	}
	p := &piece{s, 0}
	next := &piece{s, 1}
	if _, err := next.WriteAt([]byte("x"), 0); err == nil {
		t.Fatal("inactive piece accepted data")
	}
	data := bytes.Repeat([]byte("z"), 32768)
	if _, err := p.WriteAt(data, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.copyVerified(0, make([]byte, 1), 0); err == nil {
		t.Fatal("unverified payload escaped")
	}
	if err := p.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	if err := s.copyVerified(0, out, 0); err != nil || !bytes.Equal(out, data) {
		t.Fatal("verified read failed", err)
	}
	s.release()
	if p.Completion().Complete {
		t.Fatal("released piece remained complete")
	}
	for i := 1; i < 100; i++ {
		if err := s.begin(i, 32768); err != nil {
			t.Fatal(err)
		}
	}
	if s.peak != 32768 {
		t.Fatalf("memory grew with torrent size: %d", s.peak)
	}
	if err := s.begin(100, MaxPiece+1); err == nil {
		t.Fatal("oversized piece accepted")
	}
}

func TestRealTorrentPeerToBoundedRAM(t *testing.T) {
	data := bytes.Repeat([]byte("Public domain test data.\n"), 40000)
	mi := testkit.Metainfo(data, 64<<10)
	seed := testkit.StartSeeder(t, data, mi)
	ram := NewStore()
	config := NewClientConfig(ram)
	config.NoDHT = true
	config.DisableTrackers = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec, _ := torrent.TorrentSpecFromMetaInfoErr(mi)
	spec.DisallowDataDownload = true
	download, err := Add(context.Background(), c, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Drop()
	download.AddClientPeer(seed)
	source := NewPieceSource(download, ram)
	download.AllowDataDownload()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for off := 0; off < len(data); {
		n := min(77777, len(data)-off)
		out := make([]byte, n)
		if err = source.ReadAt(ctx, out, int64(off)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, data[off:off+n]) {
			t.Fatalf("torrent contents differ at %d", off)
		}
		off += n
	}
	if ram.peak > 64<<10 {
		t.Fatalf("RAM window grew to %d", ram.peak)
	}
}

func TestMetadataRejectsUnsafePathsAndLimits(t *testing.T) {
	mi := testkit.Metainfo([]byte("legal test"), 16384)
	info, _ := mi.UnmarshalInfo()
	for _, name := range []string{"../x", "..", "x/y", "x\\y", "\x00"} {
		v := info
		v.Name = name
		if validateInfo(&v) == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	info.PieceLength = MaxPiece + 1
	if validateInfo(&info) == nil {
		t.Fatal("accepted oversized pieces")
	}
	if _, err := ReadMetainfo(make([]byte, MaxMetadata+1)); err == nil {
		t.Fatal("accepted oversized metadata")
	}
}

func TestCorruptTorrentPieceIsRejected(t *testing.T) {
	expected := bytes.Repeat([]byte("verified original"), 200)
	mi := testkit.Metainfo(expected, 16<<10)
	seed := testkit.StartSeeder(t, bytes.Repeat([]byte("x"), len(expected)), mi)
	ram := NewStore()
	config := NewClientConfig(ram)
	config.NoDHT = true
	config.DisableTrackers = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec, _ := torrent.TorrentSpecFromMetaInfoErr(mi)
	spec.DisallowDataDownload = true
	download, err := Add(context.Background(), c, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Drop()
	download.AddClientPeer(seed)
	source := NewPieceSource(download, ram)
	download.AllowDataDownload()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if source.ReadAt(ctx, make([]byte, len(expected)), 0) == nil {
		t.Fatal("corrupt torrent data passed verification")
	}
	if (&piece{ram, 0}).Completion().Complete {
		t.Fatal("corrupt piece marked complete")
	}
}

func TestV2MultiFileOffsetsAndIntegrity(t *testing.T) {
	first, second := bytes.Repeat([]byte("a"), 3000), bytes.Repeat([]byte("b"), 3000)
	h1, h2 := sha256.Sum256(first), sha256.Sum256(second)
	info := metainfo.Info{Name: "v2-legal", MetaVersion: 2, PieceLength: 16 << 10, FileTree: metainfo.FileTree{Dir: map[string]metainfo.FileTree{
		"first.txt":  {File: metainfo.FileTreeFile{Length: 3000, PiecesRoot: string(h1[:])}},
		"second.txt": {File: metainfo.FileTreeFile{Length: 3000, PiecesRoot: string(h2[:])}},
	}}}
	encoded, err := bencode.Marshal(&info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: encoded}
	data := make([]byte, (16<<10)+3000)
	copy(data, first)
	copy(data[16<<10:], second)
	seed := testkit.StartSeeder(t, data, mi)
	ram := NewStore()
	config := NewClientConfig(ram)
	config.NoDHT = true
	config.DisableTrackers = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatal(err)
	}
	spec.DisallowDataDownload = true
	download, err := Add(context.Background(), c, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Drop()
	download.AddClientPeer(seed)
	source := NewPieceSource(download, ram)
	download.AllowDataDownload()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, file := range download.Files() {
		out := make([]byte, file.Length())
		if err = source.ReadAt(ctx, out, file.Offset()); err != nil {
			t.Fatal(err)
		}
		wanted := first
		if i == 1 {
			wanted = second
		}
		if !bytes.Equal(out, wanted) {
			t.Fatal("v2 file differs")
		}
	}
}

func Test200GiBMetadataFitsWithoutPayloadAllocation(t *testing.T) {
	info := metainfo.Info{Name: "large.bin", Length: 200 << 30, PieceLength: 16 << 20, Pieces: make([]byte, (200<<30)/(16<<20)*20)}
	if err := validateInfo(&info); err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	if _, err := s.OpenTorrent(context.Background(), &info, metainfo.Hash{}); err != nil {
		t.Fatal(err)
	}
	if cap(s.data) != 0 {
		t.Fatal("opening a large torrent allocated payload storage")
	}
	if err := s.begin(0, int(info.PieceLength)); err != nil {
		t.Fatal(err)
	}
	if s.peak != 16<<20 {
		t.Fatal("large torrent exceeded one piece buffer")
	}
}

func TestMismatchedV1MetadataIsRejected(t *testing.T) {
	mi := testkit.Metainfo([]byte("legal metadata integrity test"), 16<<10)
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatal(err)
	}
	spec.InfoHash[0] ^= 0xff
	ram := NewStore()
	config := NewClientConfig(ram)
	config.NoDHT = true
	config.DisableTrackers = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = Add(ctx, c, spec); err == nil {
		t.Fatal("metadata did not match requested v1 infohash but was accepted")
	}
}

func TestLateRetiredBlocksAreDroppedWithoutFailingActivePiece(t *testing.T) {
	s := NewStore()
	s.begin(0, 16384)
	old := &piece{s, 0}
	s.release()
	s.begin(1, 16384)
	p := &PieceSource{store: s, writeErrors: make(chan error, 1)}
	if _, err := old.WriteAt([]byte("late block"), 0); !errors.Is(err, errRetiredPiece) {
		t.Fatal("expected a retired-piece write", err)
	} else {
		p.onWriteError(err)
	}
	if len(p.writeErrors) != 0 {
		t.Fatal("late cancelled block failed the active piece")
	}
	if !bytes.Equal(s.data, make([]byte, len(s.data))) {
		t.Fatal("late block changed the active piece")
	}
	future := &piece{s, 2}
	_, err := future.WriteAt([]byte("unexpected block"), 0)
	if !errors.Is(err, errInactivePiece) {
		t.Fatal("unrequested future piece accepted")
	}
	p.onWriteError(err)
	if len(p.writeErrors) != 1 {
		t.Fatal("unexpected future write did not fail safely")
	}
}

func TestMultiplePeersAcrossEvictedPieces(t *testing.T) {
	data := bytes.Repeat([]byte("Legal public-domain swarm data.\n"), 500000)
	mi := testkit.Metainfo(data, 1<<20)
	ram := NewStore()
	config := NewClientConfig(ram)
	config.NoDHT = true
	config.DisableTrackers = true
	c, err := torrent.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec, _ := torrent.TorrentSpecFromMetaInfoErr(mi)
	spec.DisallowDataDownload = true
	download, err := Add(context.Background(), c, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Drop()
	for i := 0; i < 8; i++ {
		download.AddClientPeer(testkit.StartSeeder(t, data, mi))
	}
	source := NewPieceSource(download, ram)
	download.AllowDataDownload()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	buffer := make([]byte, 3<<20)
	for off := 0; off < len(data); {
		n := min(len(buffer), len(data)-off)
		if err = source.ReadAt(ctx, buffer[:n], int64(off)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffer[:n], data[off:off+n]) {
			t.Fatal("swarm corrupted payload")
		}
		off += n
	}
	if ram.peak != 1<<20 {
		t.Fatal("swarm exceeded one piece window")
	}
	for _, off := range []int{4 << 20, 0, 8 << 20} {
		if err = source.ReadAt(ctx, buffer, int64(off)); err != nil {
			t.Fatal("rewind failed", err)
		}
		if !bytes.Equal(buffer, data[off:off+len(buffer)]) {
			t.Fatal("rewind corrupted payload")
		}
	}
	source.peerDataLimit = 1
	if err = source.ReadAt(ctx, buffer, 0); !errors.Is(err, ErrRecycleClient) {
		t.Fatal("peer-data budget did not trigger a controlled refresh", err)
	}
}
