package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"hash"
	"strconv"
	"strings"
)

const uploadChunk = 8 << 20

type verifiedSource interface {
	ReadAt(context.Context, []byte, int64) error
}

func restoreHash(h hash.Hash, b []byte) error {
	if len(b) == 0 {
		return nil
	}
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok {
		return errors.New("hash does not support checkpoints")
	}
	return u.UnmarshalBinary(b)
}

func saveHash(h hash.Hash) ([]byte, error) {
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("hash does not support checkpoints")
	}
	return m.MarshalBinary()
}

func checkpoint(f *fileState, pos int64, md, sha hash.Hash) error {
	f.Uploaded = pos
	// Hash checkpoints at block boundaries contain no pending payload bytes.
	if pos%64 != 0 {
		return nil
	}
	var err error
	f.MD5State, err = saveHash(md)
	if err != nil {
		return err
	}
	f.SHAState, err = saveHash(sha)
	if err != nil {
		return err
	}
	f.Checkpoint = pos
	return nil
}

func verifyFile(remote driveFile, f *fileState) error {
	if remote.Trashed || remote.ID != f.ID || remote.Name != f.Name || remote.Size != strconv.FormatInt(f.Size, 10) || strings.HasPrefix(remote.MimeType, "application/vnd.google-apps.") || !hasParent(remote, f.Parent) {
		return errors.New("Drive file name, size, parent, or identity does not match")
	}
	if remote.MD5 == "" || remote.MD5 != f.MD5 {
		return errors.New("Drive file MD5 does not match verified torrent data")
	}
	if remote.SHA256 != "" && remote.SHA256 != f.SHA256 {
		return errors.New("Drive file SHA-256 does not match verified torrent data")
	}
	return nil
}

func uploadFile(ctx context.Context, d *driveClient, source verifiedSource, f *fileState, save func() error) error {
	md, sha := md5.New(), sha256.New()
	if f.Checkpoint < 0 || f.Checkpoint > f.Size || f.Checkpoint%64 != 0 {
		return errors.New("invalid hash checkpoint offset")
	}
	if f.Checkpoint > 0 && (len(f.MD5State) == 0 || len(f.SHAState) == 0) {
		return errors.New("missing hash checkpoint")
	}
	if err := restoreHash(md, f.MD5State); err != nil {
		return err
	}
	if err := restoreHash(sha, f.SHAState); err != nil {
		return err
	}
	pos := f.Checkpoint
	remote, exists, err := d.get(ctx, f.ID)
	if err != nil {
		return err
	}
	if exists && f.MD5 != "" && f.SHA256 != "" {
		if err = verifyFile(remote, f); err != nil {
			return err
		}
		f.Verified = true
		f.Uploaded = f.Size
		f.Session = ""
		return save()
	}
	var cursor int64
	if exists {
		cursor = f.Size
	} else if f.Session != "" {
		cursor, err = d.cursor(ctx, f.Session, f.Size)
		if err != nil {
			var apiErr *driveError
			if !errors.As(err, &apiErr) || apiErr.code != 404 && apiErr.code != 410 {
				return err
			}
			// An expired session has no durable bytes. Reuse its generated file ID.
			f.Session = ""
			f.Checkpoint = 0
			f.MD5State = nil
			f.SHAState = nil
			f.Uploaded = 0
			f.MD5 = ""
			f.SHA256 = ""
			md.Reset()
			sha.Reset()
			pos = 0
			if err = save(); err != nil {
				return err
			}
		}
	}
	if cursor < pos {
		return errors.New("Drive cursor is behind the saved hash checkpoint")
	}
	if !exists && f.Session == "" {
		f.Session, err = d.start(ctx, f)
		if err != nil {
			return err
		}
		if err = save(); err != nil {
			return err
		}
	}
	buffer := make([]byte, uploadChunk)
	for pos < f.Size {
		n := min(int64(len(buffer)), f.Size-pos)
		if pos < cursor {
			n = min(n, cursor-pos)
		}
		data := buffer[:n]
		if err = source.ReadAt(ctx, data, f.Start+pos); err != nil {
			return err
		}
		if pos >= cursor {
			acked, putErr := d.put(ctx, f.Session, pos, f.Size, data)
			for attempt := 0; putErr != nil && attempt < 5; attempt++ {
				var apiErr *driveError
				if errors.As(putErr, &apiErr) && apiErr.code != 429 && apiErr.code < 500 {
					return putErr
				}
				if err = d.pause(ctx, backoff(attempt)); err != nil {
					return err
				}
				acked, err = d.cursor(ctx, f.Session, f.Size)
				if err != nil {
					return err
				}
				if acked > pos {
					putErr = nil
					break
				}
				acked, putErr = d.put(ctx, f.Session, pos, f.Size, data)
			}
			if putErr != nil {
				return putErr
			}
			if acked <= pos || acked > pos+n {
				return errors.New("Drive acknowledged an invalid upload offset")
			}
			n = acked - pos
			data = data[:n]
			cursor = acked
		}
		md.Write(data)
		sha.Write(data)
		pos += n
		if pos == f.Size {
			f.MD5 = hex.EncodeToString(md.Sum(nil))
			f.SHA256 = hex.EncodeToString(sha.Sum(nil))
			f.Uploaded = pos
		} else if err = checkpoint(f, pos, md, sha); err != nil {
			return err
		}
		if err = save(); err != nil {
			return err
		}
	}
	if f.Size == 0 {
		if !exists {
			if _, err = d.put(ctx, f.Session, 0, 0, nil); err != nil {
				return err
			}
		}
		f.MD5 = hex.EncodeToString(md.Sum(nil))
		f.SHA256 = hex.EncodeToString(sha.Sum(nil))
		if err = save(); err != nil {
			return err
		}
	}
	remote, exists, err = d.get(ctx, f.ID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("upload finished without a visible Drive file")
	}
	if err = verifyFile(remote, f); err != nil {
		return err
	}
	f.Verified = true
	f.Session = ""
	return save()
}
