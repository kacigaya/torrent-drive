package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const stateBudget = 128 << 20
const maxJobs = 16

type fileState struct {
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

type folderState struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	ID     string `json:"id"`
	Parent string `json:"parent"`
}

type job struct {
	PublicDiscovery bool          `json:"public_discovery,omitempty"`
	ID              string        `json:"id"`
	Folder          string        `json:"folder"`
	Magnet          string        `json:"magnet,omitempty"`
	Name            string        `json:"name,omitempty"`
	Status          string        `json:"status"`
	Created         time.Time     `json:"created"`
	Updated         time.Time     `json:"updated"`
	RetryAt         time.Time     `json:"retry_at,omitempty"`
	Attempts        int           `json:"attempts"`
	Error           string        `json:"error,omitempty"`
	Files           []fileState   `json:"files,omitempty"`
	Folders         []folderState `json:"folders,omitempty"`
}

type jobView struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Status   string       `json:"status"`
	Folder   string       `json:"folder"`
	Uploaded int64        `json:"uploaded_bytes"`
	Total    int64        `json:"total_bytes"`
	Verified int          `json:"verified_files"`
	Files    int          `json:"files"`
	Error    string       `json:"error,omitempty"`
	RetryAt  time.Time    `json:"retry_at,omitempty"`
	Results  []resultFile `json:"results,omitempty"`
}

type resultFile struct {
	Path   string `json:"path"`
	ID     string `json:"drive_id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (j *job) view() jobView {
	v := jobView{ID: j.ID, Name: j.Name, Status: j.Status, Folder: j.Folder, Files: len(j.Files), Error: j.Error, RetryAt: j.RetryAt}
	for _, f := range j.Files {
		v.Total += f.Size
		v.Uploaded += f.Uploaded
		if f.Verified {
			v.Verified++
			v.Results = append(v.Results, resultFile{f.Path, f.ID, f.Size, f.SHA256})
		}
	}
	return v
}

func (j *job) uploadedBytes() int64 {
	var total int64
	for _, file := range j.Files {
		total += file.Uploaded
	}
	return total
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func atomicWrite(path string, b []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(path + ".tmp")
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func storageAvailable(dir string, extra int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		s, err := entry.Info()
		if err != nil {
			return err
		}
		total += s.Size()
	}
	if total+extra > stateBudget {
		return errors.New("job metadata budget is full; archive completed job metadata before submitting more")
	}
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return err
	}
	if s.Bavail*uint64(s.Bsize) < 256<<20 {
		return errors.New("less than 256 MiB SSD space remains; service stopped safely")
	}
	return nil
}

func saveJob(dir string, j *job) error {
	j.Updated = time.Now().UTC()
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(b) > maxMetadata {
		return errors.New("job manifest exceeds 8 MiB")
	}
	if err = storageAvailable(dir, int64(len(b))); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, j.ID+".json"), b)
}

// removeJob forgets a job on this server. Files in Google Drive stay.
// The manifest goes first, so a failure leaves at most an unused .torrent file.
func removeJob(dir, id string) error {
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, id+".torrent")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readJobs(dir string) ([]*job, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var jobs []*job
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var j job
		if len(b) > maxMetadata || json.Unmarshal(b, &j) != nil || j.ID+".json" != e.Name() || len(j.ID) != 32 || !validID(j.ID) {
			return nil, errors.New("invalid saved job metadata")
		}
		jobs = append(jobs, &j)
	}
	return jobs, nil
}
