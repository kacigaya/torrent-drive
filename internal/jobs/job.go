// Package jobs defines transfer jobs and stores their manifests on disk.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/kacigaya/torrent-drive/internal/drive"
)

// Job states, in the order a job usually moves through them.
const (
	Queued    = "queued"
	Running   = "running"
	RetryWait = "retry_wait"
	Paused    = "paused"
	Failed    = "failed"
	Complete  = "complete"
)

// Folder is a Drive folder created for a multi-file torrent.
type Folder struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	ID     string `json:"id"`
	Parent string `json:"parent"`
}

// Job is the saved manifest. It holds private data such as the magnet link
// and upload session URLs. Send only View to clients.
type Job struct {
	PublicDiscovery bool             `json:"public_discovery,omitempty"`
	ID              string           `json:"id"`
	Folder          string           `json:"folder"`
	Magnet          string           `json:"magnet,omitempty"`
	Name            string           `json:"name,omitempty"`
	Status          string           `json:"status"`
	Created         time.Time        `json:"created"`
	Updated         time.Time        `json:"updated"`
	RetryAt         time.Time        `json:"retry_at,omitempty"`
	Attempts        int              `json:"attempts"`
	Error           string           `json:"error,omitempty"`
	Files           []drive.Transfer `json:"files,omitempty"`
	Folders         []Folder         `json:"folders,omitempty"`
}

// View is the public job status for the API, CLI, and dashboard.
type View struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Folder   string    `json:"folder"`
	Uploaded int64     `json:"uploaded_bytes"`
	Total    int64     `json:"total_bytes"`
	Verified int       `json:"verified_files"`
	Files    int       `json:"files"`
	Error    string    `json:"error,omitempty"`
	RetryAt  time.Time `json:"retry_at,omitempty"`
	Results  []Result  `json:"results,omitempty"`
}

// Result is one verified file in Google Drive.
type Result struct {
	Path   string `json:"path"`
	ID     string `json:"drive_id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (j *Job) View() View {
	v := View{ID: j.ID, Name: j.Name, Status: j.Status, Folder: j.Folder, Files: len(j.Files), Error: j.Error, RetryAt: j.RetryAt}
	for _, f := range j.Files {
		v.Total += f.Size
		v.Uploaded += f.Uploaded
		if f.Verified {
			v.Verified++
			v.Results = append(v.Results, Result{f.Path, f.ID, f.Size, f.SHA256})
		}
	}
	return v
}

// Active reports whether the worker still has work for this job.
func (v View) Active() bool {
	return v.Status == Running || v.Status == Queued || v.Status == RetryWait
}

func (j *Job) UploadedBytes() int64 {
	var total int64
	for _, f := range j.Files {
		total += f.Uploaded
	}
	return total
}

// NewID returns a random 32-character hex job ID.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// ValidID reports whether s has the shape of a job ID.
func ValidID(s string) bool { return len(s) == 32 && drive.ValidID(s) }
