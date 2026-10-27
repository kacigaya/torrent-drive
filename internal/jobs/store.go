package jobs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxJobs bounds the queue, including finished jobs not yet cleared.
	MaxJobs        = 16
	maxManifest    = 8 << 20
	stateBudget    = 128 << 20
	minFreeStorage = 256 << 20
)

// Store keeps one <id>.json manifest and an optional <id>.torrent per job.
// It does no locking. The worker serializes access.
type Store struct{ Dir string }

func (s Store) manifest(id string) string { return filepath.Join(s.Dir, id+".json") }
func (s Store) torrent(id string) string  { return filepath.Join(s.Dir, id+".torrent") }

// CheckSpace fails before extra bytes would exceed the state budget or fill the disk.
func (s Store) CheckSpace(extra int64) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
	}
	if total+extra > stateBudget {
		return errors.New("job metadata budget is full; archive completed job metadata before submitting more")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(s.Dir, &fs); err != nil {
		return err
	}
	if fs.Bavail*uint64(fs.Bsize) < minFreeStorage {
		return errors.New("less than 256 MiB SSD space remains; service stopped safely")
	}
	return nil
}

func (s Store) Save(j *Job) error {
	j.Updated = time.Now().UTC()
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(b) > maxManifest {
		return errors.New("job manifest exceeds 8 MiB")
	}
	if err = s.CheckSpace(int64(len(b))); err != nil {
		return err
	}
	return atomicWrite(s.manifest(j.ID), b)
}

// SaveTorrent stores the job's torrent metadata.
func (s Store) SaveTorrent(id string, data []byte) error {
	if err := s.CheckSpace(int64(len(data))); err != nil {
		return err
	}
	return atomicWrite(s.torrent(id), data)
}

// Torrent returns saved torrent metadata. It returns os.ErrNotExist for a
// magnet whose metadata has not arrived yet.
func (s Store) Torrent(id string) ([]byte, error) { return os.ReadFile(s.torrent(id)) }

// Remove forgets a job on this server. Files in Google Drive stay.
// The manifest goes first, so a failure leaves at most an unused .torrent file.
func (s Store) Remove(id string) error {
	if err := os.Remove(s.manifest(id)); err != nil {
		return err
	}
	if err := os.Remove(s.torrent(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// List returns all jobs, oldest first. Any invalid manifest is an error,
// so the worker stops instead of skipping a job.
func (s Store) List() ([]*Job, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var list []*Job
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var j Job
		if len(b) > maxManifest || json.Unmarshal(b, &j) != nil || j.ID+".json" != e.Name() || !ValidID(j.ID) {
			return nil, errors.New("invalid saved job metadata")
		}
		list = append(list, &j)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Created.Before(list[b].Created) })
	return list, nil
}

// Find returns the job with id, or nil.
func (s Store) Find(id string) (*Job, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, j := range list {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, nil
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
