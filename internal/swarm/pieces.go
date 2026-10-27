package swarm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/anacrolix/torrent"
)

// ErrRecycleClient asks the caller to restart the torrent client. Upload
// progress is saved, so the job resumes from its Drive checkpoint.
var ErrRecycleClient = errors.New("refresh torrent client at saved Drive checkpoint")

// ErrDiscoveryRestart asks for a restart after a magnet turned out private.
var ErrDiscoveryRestart = errors.New("restart with private torrent discovery disabled")

// MaxPeerDataPerClient bounds library memory growth per client. Tests lower it.
var MaxPeerDataPerClient int64 = 4 << 30

// PieceSource reads verified torrent bytes. It downloads one piece at a time
// into the RAM store and releases the previous piece first.
type PieceSource struct {
	t             *torrent.Torrent
	store         *Store
	current       int
	writeErrors   chan error
	peerDataLimit int64
}

func NewPieceSource(t *torrent.Torrent, s *Store) *PieceSource {
	p := &PieceSource{t: t, store: s, current: -1, writeErrors: make(chan error, 1), peerDataLimit: MaxPeerDataPerClient}
	t.SetOnWriteChunkError(p.onWriteError)
	return p
}

func (p *PieceSource) checkBudget() error {
	stats := p.t.Stats()
	if stats.BytesReadData.Int64() >= p.peerDataLimit {
		return ErrRecycleClient
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if memory.HeapAlloc >= 256<<20 {
		return ErrRecycleClient
	}
	return nil
}

func (p *PieceSource) onWriteError(err error) {
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

func (p *PieceSource) selectPiece(ctx context.Context, index int) error {
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
	for !p.t.Piece(index).State().Complete {
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
	return nil
}

// ReadAt fills b with verified bytes from torrent offset off.
func (p *PieceSource) ReadAt(ctx context.Context, b []byte, off int64) error {
	info := p.t.Info()
	last := info.Piece(p.t.NumPieces() - 1)
	extent := last.Offset() + last.Length()
	if off < 0 || off > extent || int64(len(b)) > extent-off {
		return io.ErrUnexpectedEOF
	}
	for len(b) > 0 {
		index := int(off / info.PieceLength)
		if err := p.selectPiece(ctx, index); err != nil {
			return err
		}
		within := off - int64(index)*info.PieceLength
		n := min(int64(len(b)), info.Piece(index).Length()-within)
		if err := p.store.copyVerified(index, b[:n], within); err != nil {
			return err
		}
		b, off = b[n:], off+n
	}
	return nil
}
