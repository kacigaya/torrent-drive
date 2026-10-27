// Package drive is a small Google Drive v3 client for resumable uploads.
package drive

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
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
	"golang.org/x/time/rate"
)

// FolderType is the Drive MIME type of a folder.
const FolderType = "application/vnd.google-apps.folder"

const fileFields = "id,name,size,md5Checksum,sha256Checksum,parents,trashed,mimeType"

// File is the Drive metadata this service reads back.
type File struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Size     string   `json:"size"`
	MD5      string   `json:"md5Checksum"`
	SHA256   string   `json:"sha256Checksum"`
	Parents  []string `json:"parents"`
	Trashed  bool     `json:"trashed"`
	MimeType string   `json:"mimeType"`
}

func (f File) hasParent(parent string) bool {
	for _, p := range f.Parents {
		if p == parent {
			return true
		}
	}
	return false
}

type metadata struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	MimeType string   `json:"mimeType"`
	Parents  []string `json:"parents"`
}

// Credential is the private OAuth file written by the authorize command.
type Credential struct {
	ClientID     string       `json:"client_id"`
	ClientSecret string       `json:"client_secret"`
	Token        oauth2.Token `json:"token"`
}

type Client struct {
	http    *http.Client
	base    string
	upload  string
	limiter *rate.Limiter
	// Tests replace delays without changing retry or acknowledgement logic.
	pause        func(context.Context, time.Duration) error
	allowTestURL bool
}

// New loads OAuth credentials and returns a client that refreshes its token.
func New(credentialPath string) (*Client, error) {
	b, err := os.ReadFile(credentialPath)
	if err != nil {
		return nil, errors.New("cannot read OAuth credentials")
	}
	var c Credential
	if json.Unmarshal(b, &c) != nil || c.ClientID == "" || c.ClientSecret == "" || c.Token.RefreshToken == "" {
		return nil, errors.New("OAuth credentials require client_id, client_secret, and a refresh token")
	}
	base := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	config := oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: endpoints.Google}
	client := config.Client(ctx, &c.Token)
	client.Timeout = 2 * time.Minute
	client.CheckRedirect = base.CheckRedirect
	return &Client{http: client, base: "https://www.googleapis.com/drive/v3", upload: "https://www.googleapis.com/upload/drive/v3", limiter: rate.NewLimiter(16<<20, 64<<10), pause: sleep}, nil
}

// NewForTest points a client at a fake server that serves /drive and /upload.
// It skips retry delays and accepts upload sessions on that server.
func NewForTest(serverURL string, client *http.Client) *Client {
	return &Client{http: client, base: serverURL + "/drive", upload: serverURL + "/upload", allowTestURL: true, pause: func(context.Context, time.Duration) error { return nil }}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ValidID accepts Drive-style IDs. It also keeps IDs safe in URLs and file names.
func ValidID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) == -1
}

type reply struct {
	code   int
	header http.Header
	body   []byte
}

type apiError struct {
	code   int
	reason string
}

func (e *apiError) Error() string { return fmt.Sprintf("Drive HTTP %d (%s)", e.code, e.reason) }

// errorFrom keeps only a short alphabetic reason. Drive bodies can echo request data.
func errorFrom(r reply) error {
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
	return &apiError{r.code, reason}
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

// request retries transient failures. Upload bodies are never replayed here,
// because the caller must first ask Drive how many bytes it kept.
func (c *Client) request(ctx context.Context, method, target string, body []byte, headers http.Header) (reply, error) {
	for attempt := 0; attempt < 6; attempt++ {
		var reader io.Reader = bytes.NewReader(body)
		if method == "PUT" && len(body) > 0 && c.limiter != nil {
			reader = &throttledReader{ctx, bytes.NewReader(body), c.limiter}
		}
		r, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return reply{}, errors.New("invalid Drive request")
		}
		r.ContentLength = int64(len(body))
		r.Header = headers.Clone()
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("User-Agent", "torrent-drive/1")
		response, err := c.http.Do(r)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if readErr == nil {
				result := reply{response.StatusCode, response.Header, data}
				rateLimited := result.code == 403 && (strings.Contains(string(data), "rateLimitExceeded") || strings.Contains(string(data), "userRateLimitExceeded"))
				if result.code != 429 && result.code < 500 && !rateLimited {
					return result, nil
				}
			}
		}
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		if method == "PUT" && len(body) > 0 {
			return reply{}, errors.New("Drive upload response uncertain; query session before retry")
		}
		if attempt < 5 {
			delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(500))*time.Millisecond
			if err := c.pause(ctx, delay); err != nil {
				return reply{}, err
			}
		}
	}
	return reply{}, errors.New("Drive request failed after 6 attempts")
}

// Get returns a file's metadata. A missing file is not an error.
func (c *Client) Get(ctx context.Context, id string) (File, bool, error) {
	r, err := c.request(ctx, "GET", c.base+"/files/"+url.PathEscape(id)+"?supportsAllDrives=true&fields="+url.QueryEscape(fileFields), nil, nil)
	if err != nil {
		return File{}, false, err
	}
	if r.code == 404 {
		return File{}, false, nil
	}
	if r.code != 200 {
		return File{}, false, errorFrom(r)
	}
	var f File
	if json.Unmarshal(r.body, &f) != nil || f.ID == "" {
		return f, false, errors.New("invalid Drive file metadata")
	}
	return f, true, nil
}

// GenerateID reserves a file ID. Saving it first makes later creates idempotent.
func (c *Client) GenerateID(ctx context.Context) (string, error) {
	r, err := c.request(ctx, "GET", c.base+"/files/generateIds?count=1&space=drive&type=files", nil, nil)
	if err != nil {
		return "", err
	}
	if r.code != 200 {
		return "", errorFrom(r)
	}
	var v struct {
		IDs []string `json:"ids"`
	}
	if json.Unmarshal(r.body, &v) != nil || len(v.IDs) != 1 || !ValidID(v.IDs[0]) {
		return "", errors.New("invalid generated Drive ID")
	}
	return v.IDs[0], nil
}

// EnsureFolder creates a folder with a reserved ID, or checks an existing one.
func (c *Client) EnsureFolder(ctx context.Context, id, name, parent string) error {
	f, exists, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	if exists {
		if f.Trashed || f.MimeType != FolderType || f.Name != name || !f.hasParent(parent) {
			return errors.New("Drive folder identity changed")
		}
		return nil
	}
	b, _ := json.Marshal(metadata{id, name, FolderType, []string{parent}})
	r, err := c.request(ctx, "POST", c.base+"/files?supportsAllDrives=true", b, http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	switch r.code {
	case 200, 201:
		return nil
	case 409:
		return c.EnsureFolder(ctx, id, name, parent)
	default:
		return errorFrom(r)
	}
}

// Folders returns Drive's JSON list of the folders inside parent.
func (c *Client) Folders(ctx context.Context, parent string) ([]byte, error) {
	if !ValidID(parent) {
		return nil, errors.New("invalid parent folder ID")
	}
	query := "'" + parent + "' in parents and trashed = false and mimeType = '" + FolderType + "'"
	target := c.base + "/files?q=" + url.QueryEscape(query) + "&fields=nextPageToken,files(id,name)&pageSize=1000&supportsAllDrives=true&includeItemsFromAllDrives=true"
	r, err := c.request(ctx, "GET", target, nil, nil)
	if err != nil {
		return nil, err
	}
	if r.code != 200 {
		return nil, errorFrom(r)
	}
	return r.body, nil
}
