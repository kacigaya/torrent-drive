package jobs

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestStoreListsOldestFirstAndRemovesMetadata(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	newer := &Job{ID: NewID(), Status: Queued, Created: time.Now()}
	older := &Job{ID: NewID(), Status: Complete, Created: newer.Created.Add(-time.Hour)}
	for _, j := range []*Job{newer, older} {
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveTorrent(older.ID, []byte("metadata")); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil || len(list) != 2 || list[0].ID != older.ID {
		t.Fatal("jobs not listed oldest first", err)
	}
	if err = store.Remove(older.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Torrent(older.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("torrent metadata stayed after remove")
	}
	if j, err := store.Find(older.ID); err != nil || j != nil {
		t.Fatal("removed job still listed")
	}
}
