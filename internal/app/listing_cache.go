package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/ejfkdev/oss/internal/s3x"
)

// Listing cache: pages already fetched from the server are cached per
// listing fingerprint (default on), so subsequent `ls` runs are served from
// the local cache instead of re-fetching. This especially speeds up `-d`
// on buckets where directories sort after many files: the first run scans
// through them, later runs are instant.
//
// Semantics:
//   - Entries holds every raw entry fetched so far (dirs + objects, page
//     order). Cursor is the next entry to display.
//   - Pending holds entries of a page that were fetched but not displayed
//     yet (the display budget ran out mid-page). Token/V1 resume the server
//     stream after Pending.
//   - Complete marks a fully fetched listing (a replayable snapshot).
//   - When a listing exceeds maxCachedEntries, consumed entries are dropped
//     and the cache degrades to token-only continuation (still correct).

const (
	listingCacheTTL      = 24 * time.Hour
	maxCachedEntries     = 50000     // per listing
	maxListingCacheBytes = 128 << 20 // total cache file cap
)

type cachedEntry struct {
	Dir  bool   `json:"d,omitempty"`
	Key  string `json:"k"`
	Size int64  `json:"s,omitempty"`
	Mod  int64  `json:"m,omitempty"` // unix seconds
	ETag string `json:"e,omitempty"`
	SC   string `json:"c,omitempty"` // storage class
}

type listingCacheEntry struct {
	Complete  bool          `json:"c,omitempty"`
	Token     string        `json:"t,omitempty"`
	V1        bool          `json:"v1,omitempty"`
	Cursor    int           `json:"cur,omitempty"`
	Entries   []cachedEntry `json:"e"`
	Pending   []cachedEntry `json:"p,omitempty"`
	UpdatedAt time.Time     `json:"u"`

	dropped bool `json:"-"` // runtime only: entry cap reached
}

type listingCache map[string]*listingCacheEntry

// listingCacheFileOverride is used by tests to redirect the cache file.
var listingCacheFileOverride string

func listingCachePath() string {
	if listingCacheFileOverride != "" {
		return listingCacheFileOverride
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ejfkdev", "oss", "ls-cache.json")
}

func loadListingCache() listingCache {
	path := listingCachePath()
	if fi, err := os.Stat(path); err != nil || fi.Size() > maxListingCacheBytes {
		return listingCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return listingCache{}
	}
	c := listingCache{}
	if err := json.Unmarshal(data, &c); err != nil {
		return listingCache{}
	}
	now := time.Now()
	for k, e := range c {
		if e == nil || now.Sub(e.UpdatedAt) > listingCacheTTL {
			delete(c, k)
		}
	}
	return c
}

func (c listingCache) save() {
	path := listingCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// listingFingerprint identifies a listing (endpoint + region + bucket +
// prefix + delimiter + start-after) so cached data is never applied to a
// different listing.
func listingFingerprint(cl *s3x.Client, t *s3x.Target, prefix, delim, startAfter string) string {
	raw := strings.Join([]string{cl.Endpoint, cl.Region, t.Bucket, prefix, delim, startAfter}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

// pageToEntries flattens one server page into ordered cache entries
// (prefixes first, then objects — matching display order).
func pageToEntries(pg *s3x.Page) []cachedEntry {
	out := make([]cachedEntry, 0, len(pg.Prefixes)+len(pg.Objects))
	for _, p := range pg.Prefixes {
		out = append(out, cachedEntry{Dir: true, Key: p})
	}
	for _, o := range pg.Objects {
		if o.Key == nil {
			continue
		}
		e := cachedEntry{
			Key:  aws.ToString(o.Key),
			ETag: aws.ToString(o.ETag),
			SC:   string(o.StorageClass),
		}
		if o.Size != nil {
			e.Size = *o.Size
		}
		if o.LastModified != nil {
			e.Mod = o.LastModified.Unix()
		}
		out = append(out, e)
	}
	return out
}

// entryToObject converts a cached object entry back to an S3 ObjectInfo-like
// value for display.
func entryToObject(e cachedEntry) types.Object {
	o := types.Object{
		Key:          aws.String(e.Key),
		Size:         aws.Int64(e.Size),
		ETag:         aws.String(e.ETag),
		StorageClass: types.ObjectStorageClass(e.SC),
	}
	if e.Mod != 0 {
		tm := time.Unix(e.Mod, 0)
		o.LastModified = &tm
	}
	return o
}

// appendEntries adds fully consumed page entries to the cache within the
// per-listing cap; beyond it the cache degrades to token-only mode.
func (lc *listingCacheEntry) appendEntries(entries []cachedEntry) {
	if lc.dropped {
		return
	}
	room := maxCachedEntries - len(lc.Entries)
	if room <= 0 {
		lc.dropped = true
		return
	}
	if len(entries) > room {
		lc.Entries = append(lc.Entries, entries[:room]...)
		lc.dropped = true
		return
	}
	lc.Entries = append(lc.Entries, entries...)
}

// saveCompleteSnapshot stores a fully fetched listing as a replayable
// snapshot so later ls/export/download runs are served from the cache.
func saveCompleteSnapshot(cl *s3x.Client, t *s3x.Target, prefix, delimStr, startAfter string, entries []cachedEntry) {
	if len(entries) == 0 || len(entries) > maxCachedEntries {
		return
	}
	cache := loadListingCache()
	fp := listingFingerprint(cl, t, prefix, delimStr, startAfter)
	cache[fp] = &listingCacheEntry{
		Complete:  true,
		Entries:   entries,
		Cursor:    0,
		UpdatedAt: time.Now(),
	}
	cache.save()
}
