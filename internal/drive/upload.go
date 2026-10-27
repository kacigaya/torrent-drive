package drive

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ChunkSize is the largest upload body. It is the only payload buffer.
const ChunkSize = 8 << 20

// Transfer is the saved state of one torrent file and its Drive upload.
// The JSON form is stored in job manifests, so keep field tags stable.
type Transfer struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Start      int64  `json:"start"`
	Size       int64  `json:"size"`
	ID         string `json:"id"`
	Parent     string `json:"parent"`
	Session    string `json:"session,omitempty"`
	Checkpoint int64  `json:"checkpoint"`
	MD5State   []byte `json:"md5_state,omitempty"`
	SHAState   []byte `json:"sha256_state,omitempty"`
	Uploaded   int64  `json:"uploaded"`
	MD5        string `json:"md5,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Verified   bool   `json:"verified"`
}

// Source returns verified payload bytes at a torrent offset.
type Source interface {
	ReadAt(ctx context.Context, b []byte, off int64) error
}

func (c *Client) validSession(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Fragment != "" {
		return false
	}
	if c.allowTestURL {
		return strings.HasPrefix(s, c.upload+"/")
	}
	return u.Scheme == "https" && u.Host == "www.googleapis.com" && strings.HasPrefix(u.Path, "/upload/drive/")
}

func (c *Client) start(ctx context.Context, t *Transfer) (string, error) {
	b, _ := json.Marshal(metadata{t.ID, t.Name, "application/octet-stream", []string{t.Parent}})
	h := http.Header{"Content-Type": {"application/json"}, "X-Upload-Content-Type": {"application/octet-stream"}, "X-Upload-Content-Length": {strconv.FormatInt(t.Size, 10)}}
	r, err := c.request(ctx, "POST", c.upload+"/files?uploadType=resumable&supportsAllDrives=true&fields="+url.QueryEscape(fileFields), b, h)
	if err != nil {
		return "", err
	}
	if r.code != 200 && r.code != 201 {
		return "", errorFrom(r)
	}
	s := r.header.Get("Location")
	if !c.validSession(s) {
		return "", errors.New("Drive returned an untrusted upload URL")
	}
	return s, nil
}

// acknowledged returns how many bytes Drive has stored for a session.
func acknowledged(r reply, total int64) (int64, error) {
	if r.code == 200 || r.code == 201 {
		return total, nil
	}
	if r.code != 308 {
		return 0, errorFrom(r)
	}
	s := r.header.Get("Range")
	if s == "" {
		return 0, nil
	}
	if !strings.HasPrefix(s, "bytes=0-") {
		return 0, errors.New("invalid Drive acknowledgement range")
	}
	last, err := strconv.ParseInt(strings.TrimPrefix(s, "bytes=0-"), 10, 64)
	if err != nil || last < 0 || last >= total {
		return 0, errors.New("Drive acknowledgement exceeds file size")
	}
	return last + 1, nil
}

func (c *Client) cursor(ctx context.Context, session string, total int64) (int64, error) {
	if !c.validSession(session) {
		return 0, errors.New("invalid saved Drive upload URL")
	}
	r, err := c.request(ctx, "PUT", session, nil, http.Header{"Content-Range": {fmt.Sprintf("bytes */%d", total)}})
	if err != nil {
		return 0, err
	}
	return acknowledged(r, total)
}

func (c *Client) put(ctx context.Context, session string, offset, total int64, body []byte) (int64, error) {
	if !c.validSession(session) {
		return 0, errors.New("invalid saved Drive upload URL")
	}
	h := http.Header{"Content-Type": {"application/octet-stream"}, "Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(body))-1, total)}}
	if total == 0 {
		h.Set("Content-Range", "bytes */0")
	}
	r, err := c.request(ctx, "PUT", session, body, h)
	if err != nil {
		return 0, err
	}
	return acknowledged(r, total)
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

func checkpoint(t *Transfer, pos int64, md, sha hash.Hash) error {
	t.Uploaded = pos
	// Hash checkpoints at block boundaries contain no pending payload bytes.
	if pos%64 != 0 {
		return nil
	}
	var err error
	if t.MD5State, err = saveHash(md); err != nil {
		return err
	}
	if t.SHAState, err = saveHash(sha); err != nil {
		return err
	}
	t.Checkpoint = pos
	return nil
}

func matches(remote File, t *Transfer) error {
	if remote.Trashed || remote.ID != t.ID || remote.Name != t.Name || remote.Size != strconv.FormatInt(t.Size, 10) || strings.HasPrefix(remote.MimeType, "application/vnd.google-apps.") || !remote.hasParent(t.Parent) {
		return errors.New("Drive file name, size, parent, or identity does not match")
	}
	if remote.MD5 == "" || remote.MD5 != t.MD5 {
		return errors.New("Drive file MD5 does not match verified torrent data")
	}
	if remote.SHA256 != "" && remote.SHA256 != t.SHA256 {
		return errors.New("Drive file SHA-256 does not match verified torrent data")
	}
	return nil
}

// Verify checks that Drive holds the exact bytes recorded in t.
func (c *Client) Verify(ctx context.Context, t *Transfer) error {
	remote, exists, err := c.Get(ctx, t.ID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("Drive file is missing")
	}
	return matches(remote, t)
}

func retryDelay(attempt int) time.Duration { return time.Duration(1<<min(attempt, 8)) * time.Second }

// Upload sends one file from source and resumes from t when possible.
// It calls save after every state change that must survive a crash.
func (c *Client) Upload(ctx context.Context, source Source, t *Transfer, save func() error) error {
	md, sha := md5.New(), sha256.New()
	if t.Checkpoint < 0 || t.Checkpoint > t.Size || t.Checkpoint%64 != 0 {
		return errors.New("invalid hash checkpoint offset")
	}
	if t.Checkpoint > 0 && (len(t.MD5State) == 0 || len(t.SHAState) == 0) {
		return errors.New("missing hash checkpoint")
	}
	if err := restoreHash(md, t.MD5State); err != nil {
		return err
	}
	if err := restoreHash(sha, t.SHAState); err != nil {
		return err
	}
	pos := t.Checkpoint
	remote, exists, err := c.Get(ctx, t.ID)
	if err != nil {
		return err
	}
	// A crash after the last byte but before saving Verified lands here.
	if exists && t.MD5 != "" && t.SHA256 != "" {
		if err = matches(remote, t); err != nil {
			return err
		}
		t.Verified, t.Uploaded, t.Session = true, t.Size, ""
		return save()
	}
	var cursor int64
	if exists {
		cursor = t.Size
	} else if t.Session != "" {
		cursor, err = c.cursor(ctx, t.Session, t.Size)
		if err != nil {
			var apiErr *apiError
			if !errors.As(err, &apiErr) || apiErr.code != 404 && apiErr.code != 410 {
				return err
			}
			// An expired session has no durable bytes. Reuse its generated file ID.
			*t = Transfer{Path: t.Path, Name: t.Name, Start: t.Start, Size: t.Size, ID: t.ID, Parent: t.Parent}
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
	if !exists && t.Session == "" {
		if t.Session, err = c.start(ctx, t); err != nil {
			return err
		}
		if err = save(); err != nil {
			return err
		}
	}
	buffer := make([]byte, ChunkSize)
	for pos < t.Size {
		n := min(int64(len(buffer)), t.Size-pos)
		if pos < cursor {
			// Bytes Drive already has only need to pass through the hashes.
			n = min(n, cursor-pos)
		}
		data := buffer[:n]
		if err = source.ReadAt(ctx, data, t.Start+pos); err != nil {
			return err
		}
		if pos >= cursor {
			acked, err := c.send(ctx, t, pos, data)
			if err != nil {
				return err
			}
			n = acked - pos
			data = data[:n]
			cursor = acked
		}
		md.Write(data)
		sha.Write(data)
		pos += n
		if pos == t.Size {
			t.MD5 = hex.EncodeToString(md.Sum(nil))
			t.SHA256 = hex.EncodeToString(sha.Sum(nil))
			t.Uploaded = pos
		} else if err = checkpoint(t, pos, md, sha); err != nil {
			return err
		}
		if err = save(); err != nil {
			return err
		}
	}
	if t.Size == 0 {
		if !exists {
			if _, err = c.put(ctx, t.Session, 0, 0, nil); err != nil {
				return err
			}
		}
		t.MD5 = hex.EncodeToString(md.Sum(nil))
		t.SHA256 = hex.EncodeToString(sha.Sum(nil))
		if err = save(); err != nil {
			return err
		}
	}
	if err = c.Verify(ctx, t); err != nil {
		return err
	}
	t.Verified, t.Session = true, ""
	return save()
}

// send uploads one chunk and returns the new acknowledged offset. After an
// uncertain response it asks Drive for the cursor before it sends again.
func (c *Client) send(ctx context.Context, t *Transfer, pos int64, data []byte) (int64, error) {
	acked, err := c.put(ctx, t.Session, pos, t.Size, data)
	for attempt := 0; err != nil && attempt < 5; attempt++ {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.code != 429 && apiErr.code < 500 {
			return 0, err
		}
		if pauseErr := c.pause(ctx, retryDelay(attempt)); pauseErr != nil {
			return 0, pauseErr
		}
		if acked, err = c.cursor(ctx, t.Session, t.Size); err != nil {
			return 0, err
		}
		if acked > pos {
			break
		}
		acked, err = c.put(ctx, t.Session, pos, t.Size, data)
	}
	if err != nil {
		return 0, err
	}
	if acked <= pos || acked > pos+int64(len(data)) {
		return 0, errors.New("Drive acknowledged an invalid upload offset")
	}
	return acked, nil
}
