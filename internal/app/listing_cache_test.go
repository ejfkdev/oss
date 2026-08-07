package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"oss/internal/s3x"
)

func TestPageToEntriesOrder(t *testing.T) {
	now := time.Now()
	pg := &s3x.Page{
		Prefixes: []string{"dir1/", "dir2/"},
		Objects: []types.Object{
			{Key: aws.String("f1"), Size: aws.Int64(5), LastModified: &now, ETag: aws.String("\"e1\"")},
			{Key: nil}, // must be skipped
			{Key: aws.String("f2"), Size: aws.Int64(7)},
		},
	}
	es := pageToEntries(pg)
	if len(es) != 4 {
		t.Fatalf("want 4 entries, got %d", len(es))
	}
	if !es[0].Dir || es[0].Key != "dir1/" || !es[1].Dir || es[1].Key != "dir2/" {
		t.Errorf("prefixes must come first: %+v", es[:2])
	}
	if es[2].Key != "f1" || es[2].Size != 5 || es[2].ETag != "\"e1\"" || es[2].Mod == 0 {
		t.Errorf("object entry wrong: %+v", es[2])
	}
	if es[3].Key != "f2" || es[3].Size != 7 {
		t.Errorf("object entry wrong: %+v", es[3])
	}
	// Round-trip back to a display object.
	o := entryToObject(es[2])
	if aws.ToString(o.Key) != "f1" || aws.ToInt64(o.Size) != 5 || o.LastModified == nil {
		t.Errorf("entryToObject wrong: %+v", o)
	}
}

func TestAppendEntriesCap(t *testing.T) {
	lc := &listingCacheEntry{}
	batch := make([]cachedEntry, maxCachedEntries-5)
	for i := range batch {
		batch[i] = cachedEntry{Key: "k"}
	}
	lc.appendEntries(batch)
	if lc.dropped {
		t.Fatal("should not be dropped yet")
	}
	lc.appendEntries(make([]cachedEntry, 10)) // crosses the cap
	if !lc.dropped {
		t.Fatal("should be dropped after crossing the cap")
	}
	if len(lc.Entries) != maxCachedEntries {
		t.Fatalf("entries should be capped at %d, got %d", maxCachedEntries, len(lc.Entries))
	}
	lc.appendEntries([]cachedEntry{{Key: "more"}})
	if len(lc.Entries) != maxCachedEntries {
		t.Fatal("dropped cache must not grow")
	}
}

func TestCacheSaveLoadRoundTrip(t *testing.T) {
	listingCacheFileOverride = filepath.Join(t.TempDir(), "ls-cache.json")
	defer func() { listingCacheFileOverride = "" }()

	c := listingCache{
		"fp1": {
			Complete: true,
			Cursor:   2,
			Entries: []cachedEntry{
				{Dir: true, Key: "d/"},
				{Key: "f1", Size: 3, Mod: 1700000000},
			},
			UpdatedAt: time.Now(),
		},
		"fp2": {
			Token:   "tok",
			V1:      true,
			Pending: []cachedEntry{{Key: "p"}},
			Entries: []cachedEntry{},
			UpdatedAt: time.Now(),
		},
	}
	c.save()

	c2 := loadListingCache()
	e1, e2 := c2["fp1"], c2["fp2"]
	if e1 == nil || e2 == nil {
		t.Fatalf("entries lost: %+v", c2)
	}
	if !e1.Complete || e1.Cursor != 2 || len(e1.Entries) != 2 || !e1.Entries[0].Dir || e1.Entries[1].Size != 3 {
		t.Errorf("fp1 wrong: %+v", e1)
	}
	if e2.Token != "tok" || !e2.V1 || len(e2.Pending) != 1 || e2.Pending[0].Key != "p" {
		t.Errorf("fp2 wrong: %+v", e2)
	}

	// Expired entries are pruned on load.
	c["fp1"].UpdatedAt = time.Now().Add(-listingCacheTTL - time.Hour)
	c.save()
	c3 := loadListingCache()
	if _, ok := c3["fp1"]; ok {
		t.Error("expired entry should be pruned")
	}
	if _, ok := c3["fp2"]; !ok {
		t.Error("fresh entry should survive")
	}
}
