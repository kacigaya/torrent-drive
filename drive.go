package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
	"golang.org/x/time/rate"
)

const driveFields = "id,name,size,md5Checksum,sha256Checksum,parents,trashed,mimeType"

type driveFile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Size     string   `json:"size"`
	MD5      string   `json:"md5Checksum"`
	SHA256   string   `json:"sha256Checksum"`
	Parents  []string `json:"parents"`
	Trashed  bool     `json:"trashed"`
	MimeType string   `json:"mimeType"`
}

type driveMetadata struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	MimeType string   `json:"mimeType"`
	Parents  []string `json:"parents"`
}

type credential struct {
	ClientID     string       `json:"client_id"`
	ClientSecret string       `json:"client_secret"`
	Token        oauth2.Token `json:"token"`
}

type driveClient struct {
	http    *http.Client
	base    string
	upload  string
	limiter *rate.Limiter
	// Tests replace delays without changing retry or acknowledgement logic.
	pause        func(context.Context, time.Duration) error
	allowTestURL bool
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newDrive(path string) (*driveClient, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read OAuth credentials")
	}
	var c credential
	if json.Unmarshal(b, &c) != nil || c.ClientID == "" || c.ClientSecret == "" || c.Token.RefreshToken == "" {
		return nil, errors.New("OAuth credentials require client_id, client_secret, and a refresh token")
	}
	base := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	config := oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: endpoints.Google}
	client := config.Client(ctx, &c.Token)
	client.Timeout = 2 * time.Minute
	client.CheckRedirect = base.CheckRedirect
	return &driveClient{http: client, base: "https://www.googleapis.com/drive/v3", upload: "https://www.googleapis.com/upload/drive/v3", limiter: rate.NewLimiter(16<<20, 64<<10), pause: sleepContext}, nil
}

type driveReply struct {
	code   int
	header http.Header
	body   []byte
}
type driveError struct {
	code   int
	reason string
}

func (e *driveError) Error() string { return fmt.Sprintf("Drive HTTP %d (%s)", e.code, e.reason) }

func errorFromReply(r driveReply) error {
	var body struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &body)
	reason := "request failed"
	if len(body.Error.Errors) > 0 {
		candidate := body.Error.Errors[0].Reason
		if len(candidate) < 80 && strings.IndexFunc(candidate, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') }) == -1 {
			reason = candidate
		}
	}
	return &driveError{r.code, reason}
}

type throttledReader struct {
	ctx     context.Context
	r       *bytes.Reader
	limiter *rate.Limiter
}

func (r *throttledReader) Read(b []byte) (int, error) {
	n := min(len(b), r.limiter.Burst(), r.r.Len())
	if n == 0 {
		return 0, io.EOF
	}
	if err := r.limiter.WaitN(r.ctx, n); err != nil {
		return 0, err
	}
	return r.r.Read(b[:n])
}

func (d *driveClient) request(ctx context.Context, method, target string, body []byte, headers http.Header) (driveReply, error) {
	for attempt := 0; attempt < 6; attempt++ {
		var reader io.Reader = bytes.NewReader(body)
		if method == "PUT" && len(body) > 0 && d.limiter != nil {
			reader = &throttledReader{ctx, bytes.NewReader(body), d.limiter}
		}
		r, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return driveReply{}, errors.New("invalid Drive request")
		}
		r.ContentLength = int64(len(body))
		r.Header = headers.Clone()
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("User-Agent", "torrent-drive/1")
		response, err := d.http.Do(r)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if readErr == nil {
				reply := driveReply{response.StatusCode, response.Header, data}
				if reply.code != 429 && reply.code < 500 {
					if reply.code != 403 || !strings.Contains(string(data), "rateLimitExceeded") && !strings.Contains(string(data), "userRateLimitExceeded") {
						return reply, nil
					}
				}
			}
		}
		if ctx.Err() != nil {
			return driveReply{}, ctx.Err()
		}
		// PUT requests must be reconciled with the session cursor before replay.
		if method == "PUT" && len(body) > 0 {
			return driveReply{}, errors.New("Drive upload response uncertain; query session before retry")
		}
		if attempt < 5 {
			delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(500))*time.Millisecond
			if err := d.pause(ctx, delay); err != nil {
				return driveReply{}, err
			}
		}
	}
	return driveReply{}, errors.New("Drive request failed after 6 attempts")
}

func (d *driveClient) get(ctx context.Context, id string) (driveFile, bool, error) {
	r, err := d.request(ctx, "GET", d.base+"/files/"+url.PathEscape(id)+"?supportsAllDrives=true&fields="+url.QueryEscape(driveFields), nil, nil)
	if err != nil {
		return driveFile{}, false, err
	}
	if r.code == 404 {
		return driveFile{}, false, nil
	}
	if r.code != 200 {
		return driveFile{}, false, errorFromReply(r)
	}
	var f driveFile
	if json.Unmarshal(r.body, &f) != nil || f.ID == "" {
		return f, false, errors.New("invalid Drive file metadata")
	}
	return f, true, nil
}

func (d *driveClient) generateID(ctx context.Context) (string, error) {
	r, err := d.request(ctx, "GET", d.base+"/files/generateIds?count=1&space=drive&type=files", nil, nil)
	if err != nil {
		return "", err
	}
	if r.code != 200 {
		return "", errorFromReply(r)
	}
	var v struct {
		IDs []string `json:"ids"`
	}
	if json.Unmarshal(r.body, &v) != nil || len(v.IDs) != 1 || !validID(v.IDs[0]) {
		return "", errors.New("invalid generated Drive ID")
	}
	return v.IDs[0], nil
}

func (d *driveClient) ensureFolder(ctx context.Context, id, name, parent string) error {
	f, exists, err := d.get(ctx, id)
	if err != nil {
		return err
	}
	if exists {
		if f.Trashed || f.MimeType != "application/vnd.google-apps.folder" || f.Name != name || !hasParent(f, parent) {
			return errors.New("Drive folder identity changed")
		}
		return nil
	}
	b, _ := json.Marshal(driveMetadata{id, name, "application/vnd.google-apps.folder", []string{parent}})
	r, err := d.request(ctx, "POST", d.base+"/files?supportsAllDrives=true", b, http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	if r.code != 200 && r.code != 201 && r.code != 409 {
		return errorFromReply(r)
	}
	if r.code == 409 {
		return d.ensureFolder(ctx, id, name, parent)
	}
	return nil
}

func hasParent(f driveFile, parent string) bool {
	for _, p := range f.Parents {
		if p == parent {
			return true
		}
	}
	return false
}
func validID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) == -1
}

func (d *driveClient) validateSession(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Fragment != "" {
		return false
	}
	if d.allowTestURL {
		return strings.HasPrefix(s, d.upload+"/")
	}
	return u.Scheme == "https" && u.Host == "www.googleapis.com" && strings.HasPrefix(u.Path, "/upload/drive/")
}

func (d *driveClient) start(ctx context.Context, f *fileState) (string, error) {
	b, _ := json.Marshal(driveMetadata{f.ID, f.Name, "application/octet-stream", []string{f.Parent}})
	h := http.Header{"Content-Type": {"application/json"}, "X-Upload-Content-Type": {"application/octet-stream"}, "X-Upload-Content-Length": {strconv.FormatInt(f.Size, 10)}}
	r, err := d.request(ctx, "POST", d.upload+"/files?uploadType=resumable&supportsAllDrives=true&fields="+url.QueryEscape(driveFields), b, h)
	if err != nil {
		return "", err
	}
	if r.code != 200 && r.code != 201 {
		return "", errorFromReply(r)
	}
	s := r.header.Get("Location")
	if !d.validateSession(s) {
		return "", errors.New("Drive returned an untrusted upload URL")
	}
	return s, nil
}

func acknowledged(r driveReply, total int64) (int64, error) {
	if r.code == 200 || r.code == 201 {
		return total, nil
	}
	if r.code != 308 {
		return 0, errorFromReply(r)
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

func (d *driveClient) cursor(ctx context.Context, session string, total int64) (int64, error) {
	if !d.validateSession(session) {
		return 0, errors.New("invalid saved Drive upload URL")
	}
	r, err := d.request(ctx, "PUT", session, nil, http.Header{"Content-Range": {fmt.Sprintf("bytes */%d", total)}})
	if err != nil {
		return 0, err
	}
	return acknowledged(r, total)
}

func (d *driveClient) put(ctx context.Context, session string, offset, total int64, body []byte) (int64, error) {
	if !d.validateSession(session) {
		return 0, errors.New("invalid saved Drive upload URL")
	}
	end := offset + int64(len(body)) - 1
	h := http.Header{"Content-Type": {"application/octet-stream"}, "Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, end, total)}}
	if total == 0 {
		h.Set("Content-Range", "bytes */0")
	}
	r, err := d.request(ctx, "PUT", session, body, h)
	if err != nil {
		return 0, err
	}
	return acknowledged(r, total)
}
