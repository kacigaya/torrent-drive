package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const maxPiece = 64 << 20

var errInactivePiece = errors.New("piece is outside the RAM window")
var errRetiredPiece = errors.New("piece was evicted from the RAM window")

// Only the requested piece can allocate payload memory. No storage path exists.
type ramStore struct {
	mu       sync.Mutex
	index    int
	retired  int
	data     []byte
	complete bool
	peak     int
}

func newRAMStore() *ramStore { return &ramStore{index: -1, retired: -1} }

func (s *ramStore) OpenTorrent(_ context.Context, info *metainfo.Info, _ metainfo.Hash) (storage.TorrentImpl, error) {
	if err := validateInfo(info); err != nil {
		return storage.TorrentImpl{}, err
	}
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl { return &ramPiece{s: s, index: p.Index()} },
		Close: func() error { s.release(); return nil },
	}, nil
}

func (s *ramStore) begin(index, size int) error {
	if size <= 0 || size > maxPiece {
		return errors.New("piece exceeds the 64 MiB RAM limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cap(s.data) < size {
		s.data = make([]byte, size)
	} else {
		s.data = s.data[:size]
		clear(s.data)
	}
	s.index, s.complete = index, false
	s.peak = max(s.peak, cap(s.data))
	return nil
}

func (s *ramStore) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retired = max(s.retired, s.index)
	s.index, s.complete = -1, false
	clear(s.data)
}

func (s *ramStore) copyVerified(index int, dst []byte, off int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index != index || !s.complete {
		return errors.New("piece has not passed torrent verification")
	}
	if off < 0 || off > int64(len(s.data)) || int64(len(dst)) > int64(len(s.data))-off {
		return io.ErrUnexpectedEOF
	}
	copy(dst, s.data[off:off+int64(len(dst))])
	return nil
}

type ramPiece struct {
	s     *ramStore
	index int
}

func (p *ramPiece) ReadAt(b []byte, off int64) (int, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if p.s.index != p.index {
		return 0, io.EOF
	}
	if off < 0 || off >= int64(len(p.s.data)) {
		return 0, io.EOF
	}
	n := copy(b, p.s.data[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

func (p *ramPiece) WriteAt(b []byte, off int64) (int, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if p.s.index != p.index {
		kind := errInactivePiece
		if p.index <= p.s.retired {
			kind = errRetiredPiece
		}
		return 0, fmt.Errorf("%w: received %d, active %d, retired through %d", kind, p.index, p.s.index, p.s.retired)
	}
	if off < 0 || off > int64(len(p.s.data)) || int64(len(b)) > int64(len(p.s.data))-off {
		return 0, io.ErrShortWrite
	}
	return copy(p.s.data[off:], b), nil
}

func (p *ramPiece) MarkComplete() error {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if p.s.index != p.index {
		return errInactivePiece
	}
	p.s.complete = true
	return nil
}

func (p *ramPiece) MarkNotComplete() error {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if p.s.index == p.index {
		p.s.complete = false
	}
	return nil
}

func (p *ramPiece) Completion() storage.Completion {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return storage.Completion{Ok: true, Complete: p.s.index == p.index && p.s.complete}
}
