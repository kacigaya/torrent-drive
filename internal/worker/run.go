package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/kacigaya/torrent-drive/internal/drive"
	"github.com/kacigaya/torrent-drive/internal/jobs"
	"github.com/kacigaya/torrent-drive/internal/swarm"
)

// loop runs one job at a time until ctx ends. It returns an error when job
// state cannot be saved, or swarm.ErrRecycleClient to request a restart.
func (s *Service) loop(ctx context.Context) error {
	for ctx.Err() == nil {
		s.mu.Lock()
		j, err := s.next()
		if err != nil {
			s.mu.Unlock()
			return errors.New("cannot read job state; worker stopped")
		}
		if j == nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		j.Status, j.Error = jobs.Running, ""
		jobCtx, cancel := context.WithCancel(ctx)
		s.active, s.cancel = j.ID, cancel
		err = s.store.Save(j)
		startUploaded := j.UploadedBytes()
		s.mu.Unlock()
		if err == nil {
			err = s.runJob(jobCtx, j)
		}
		s.mu.Lock()
		interrupted := jobCtx.Err() != nil
		cancel()
		s.active, s.cancel = "", nil
		finishAttempt(j, err, ctx.Err() != nil, interrupted, startUploaded)
		saveErr := s.store.Save(j)
		s.mu.Unlock()
		if saveErr != nil {
			return errors.New("cannot save job result; worker stopped")
		}
		log.Printf("job %s: %s", j.ID, j.Status)
		if errors.Is(err, swarm.ErrRecycleClient) || errors.Is(err, swarm.ErrDiscoveryRestart) {
			return swarm.ErrRecycleClient
		}
	}
	return nil
}

// next returns the oldest job that is ready to run, or nil.
func (s *Service) next() (*jobs.Job, error) {
	list, err := s.store.List()
	if err != nil {
		return nil, err
	}
	for _, j := range list {
		if j.Status == jobs.Queued || j.Status == jobs.Running || j.Status == jobs.RetryWait && !time.Now().Before(j.RetryAt) {
			return j, nil
		}
	}
	return nil, nil
}

func backoff(attempt int) time.Duration { return time.Duration(1<<min(attempt, 8)) * time.Second }

// finishAttempt moves a job to its next state after one run.
// stopping means the service is shutting down; interrupted means the job's
// own context ended, from a pause request or from shutdown.
func finishAttempt(j *jobs.Job, err error, stopping, interrupted bool, startUploaded int64) {
	pausedOrQueued := jobs.Paused
	if stopping {
		pausedOrQueued = jobs.Queued
	}
	j.RetryAt = time.Time{}
	if err != nil && interrupted {
		j.Status, j.Error = pausedOrQueued, ""
		return
	}
	// A refresh that made no progress would loop forever. Count it as a failure.
	if errors.Is(err, swarm.ErrRecycleClient) && j.UploadedBytes() <= startUploaded {
		err = errors.New("torrent client resource budget reached without new upload progress")
	}
	switch {
	case err == nil:
		j.Status, j.Error = jobs.Complete, ""
	case errors.Is(err, swarm.ErrRecycleClient) || errors.Is(err, swarm.ErrDiscoveryRestart):
		j.Status, j.Error = jobs.Queued, ""
	case errors.Is(err, context.Canceled):
		j.Status, j.Error = pausedOrQueued, ""
	default:
		j.Error = err.Error()
		j.Attempts++
		j.Status = jobs.RetryWait
		j.RetryAt = time.Now().Add(30 * backoff(j.Attempts-1))
		if j.Attempts >= 8 {
			j.Status = jobs.Failed
		}
	}
}

func (s *Service) runJob(ctx context.Context, j *jobs.Job) error {
	folder, exists, err := s.drive.Get(ctx, j.Folder)
	if err != nil {
		return err
	}
	if !exists || folder.Trashed || folder.MimeType != drive.FolderType {
		return errors.New("Drive destination folder is missing or invalid")
	}
	allowPublic, knownMetadata, err := s.discoveryPolicy(j)
	if err != nil {
		return err
	}
	ram := swarm.NewStore()
	config := swarm.NewClientConfig(ram)
	config.NoDHT = !allowPublic
	config.DisablePEX = !allowPublic
	// Keep the same peer ID across controlled client refreshes.
	identity := sha256.Sum256([]byte(j.ID))
	var peerID [20]byte
	n := copy(peerID[:], config.Bep20)
	copy(peerID[n:], identity[:])
	config.PeerID = string(peerID[:])
	client, err := torrent.NewClient(config)
	if err != nil {
		return errors.New("cannot start RAM torrent client")
	}
	defer client.Close()
	t, err := s.loadTorrent(ctx, j, client)
	if err != nil {
		return err
	}
	defer t.Drop()
	// A magnet that resolves to a private torrent must not keep using DHT or PEX.
	if !knownMetadata && allowPublic && swarm.Private(t.Info()) {
		return swarm.ErrDiscoveryRestart
	}
	j.Name = t.Name()
	if len(j.Files) == 0 {
		for _, f := range t.Files() {
			if strings.Contains(f.FileInfo().Attr, "p") {
				continue // BEP 47 padding file
			}
			j.Files = append(j.Files, drive.Transfer{Path: f.Path(), Name: path.Base(f.Path()), Start: f.Offset(), Size: f.Length()})
		}
		if len(j.Files) == 0 {
			return errors.New("torrent contains no ordinary files")
		}
		if err = s.save(j); err != nil {
			return err
		}
	}
	if err = s.prepareFolders(ctx, j); err != nil {
		return err
	}
	source := swarm.NewPieceSource(t, ram)
	t.AllowDataDownload()
	save := func() error { return s.save(j) }
	for i := range j.Files {
		if !j.Files[i].Verified {
			if err = s.drive.Upload(ctx, source, &j.Files[i], save); err != nil {
				return err
			}
		}
	}
	// Check every file again, so a file changed in Drive during the job fails it.
	for i := range j.Files {
		if err = s.drive.Verify(ctx, &j.Files[i]); err != nil {
			return err
		}
	}
	return nil
}

// loadTorrent adds the job's torrent and saves its metadata, so a magnet
// needs peers for metadata only once.
func (s *Service) loadTorrent(ctx context.Context, j *jobs.Job, client *torrent.Client) (*torrent.Torrent, error) {
	var spec *torrent.TorrentSpec
	b, err := s.store.Torrent(j.ID)
	switch {
	case err == nil:
		mi, err := swarm.ReadMetainfo(b)
		if err != nil {
			return nil, err
		}
		if spec, err = torrent.TorrentSpecFromMetaInfoErr(mi); err != nil {
			return nil, errors.New("invalid torrent specification")
		}
	case os.IsNotExist(err) && j.Magnet != "":
		if spec, err = torrent.TorrentSpecFromMagnetUri(j.Magnet); err != nil {
			return nil, errors.New("invalid saved magnet")
		}
	default:
		return nil, errors.New("missing torrent metadata")
	}
	// Sources and webseeds can download outside the piece scheduler. Keep them off.
	spec.Sources = nil
	spec.Webseeds = nil
	spec.DisallowDataDownload = true
	spec.DisallowDataUpload = true
	spec.DisableInitialPieceCheck = true
	t, err := swarm.Add(ctx, client, spec)
	if err != nil {
		return nil, err
	}
	var metadata bytes.Buffer
	mi := t.Metainfo()
	if err = mi.Write(&metadata); err != nil {
		return nil, err
	}
	if metadata.Len() > swarm.MaxMetadata {
		return nil, errors.New("magnet metadata exceeds 8 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.store.SaveTorrent(j.ID, metadata.Bytes()); err != nil {
		return nil, err
	}
	return t, nil
}

// discoveryPolicy decides whether DHT and PEX are allowed. Known metadata
// decides by its private flag. An unknown magnet follows the user's choice.
func (s *Service) discoveryPolicy(j *jobs.Job) (allowPublic, known bool, err error) {
	b, err := s.store.Torrent(j.ID)
	if os.IsNotExist(err) {
		return j.PublicDiscovery, false, nil
	}
	if err != nil {
		return false, false, errors.New("cannot read torrent discovery policy")
	}
	mi, err := swarm.ReadMetainfo(b)
	if err != nil {
		return false, false, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return false, false, errors.New("invalid torrent discovery metadata")
	}
	return !swarm.Private(&info), true, nil
}

// prepareFolders reserves Drive IDs for every folder and file, saving each ID
// before use, then creates the folders. A restart reuses the same IDs.
func (s *Service) prepareFolders(ctx context.Context, j *jobs.Job) error {
	ids := map[string]string{".": j.Folder}
	for _, f := range j.Folders {
		ids[f.Path] = f.ID
	}
	for i := range j.Files {
		f := &j.Files[i]
		dir := path.Dir(f.Path)
		var parents []string
		for p := dir; p != "."; p = path.Dir(p) {
			parents = append(parents, p)
		}
		for k := len(parents) - 1; k >= 0; k-- {
			p := parents[k]
			if ids[p] != "" {
				continue
			}
			id, err := s.drive.GenerateID(ctx)
			if err != nil {
				return err
			}
			j.Folders = append(j.Folders, jobs.Folder{Path: p, Name: path.Base(p), ID: id, Parent: ids[path.Dir(p)]})
			ids[p] = id
			if err = s.save(j); err != nil {
				return err
			}
		}
		f.Parent = ids[dir]
		if f.ID == "" {
			id, err := s.drive.GenerateID(ctx)
			if err != nil {
				return err
			}
			f.ID = id
			if err = s.save(j); err != nil {
				return err
			}
		}
	}
	for _, f := range j.Folders {
		if err := s.drive.EnsureFolder(ctx, f.ID, f.Name, f.Parent); err != nil {
			return err
		}
	}
	return s.save(j)
}
