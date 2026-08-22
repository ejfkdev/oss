package app

import (
	"context"
	"math"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ejfkdev/oss/internal/s3x"
)

// listParams are the resolved S3 listing parameters shared by the display,
// export and download paths so they always agree.
type listParams struct {
	prefix     string
	delim      *string // nil = flat (recursive)
	delimStr   string
	maxKeys    *int32
	startAfter string
}

// listFlags carries the raw listing options independent of any CLI framework.
// Pointer fields distinguish "explicitly set" from "left at default"
// (the CLI's IsSet semantics), so both CLI and HTTP/MCP paths resolve the
// same way.
type listFlags struct {
	recursive  bool
	delimiter  *string
	prefix     string
	pageSize   *int64
	startAfter string
	dirsOnly   bool
	filesOnly  bool
	include    []string
	exclude    []string
}

func int64Ptr(v int64) *int64 { return &v }

func resolveListParamsF(f listFlags, t *s3x.Target) listParams {
	// Delimiter resolution: recursive > explicit delimiter > URL ?delimiter= > "/".
	var delim *string
	switch {
	case f.recursive:
	case f.delimiter != nil:
		delim = f.delimiter
	case t.List.Delimiter != nil:
		delim = t.List.Delimiter
	default:
		d := "/"
		delim = &d
	}
	delimStr := ""
	if delim != nil {
		delimStr = *delim
	}

	prefix := t.Key + t.List.Prefix + f.prefix

	var maxKeys *int32
	if f.pageSize != nil {
		v := int32(min(*f.pageSize, math.MaxInt32))
		maxKeys = &v
	} else if t.List.MaxKeys > 0 {
		maxKeys = &t.List.MaxKeys
	}

	startAfter := f.startAfter
	if startAfter == "" {
		startAfter = t.List.StartAfter
	}
	return listParams{prefix: prefix, delim: delim, delimStr: delimStr, maxKeys: maxKeys, startAfter: startAfter}
}

func (p listParams) v2Input(bucket, token string) *s3.ListObjectsV2Input {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}
	if p.prefix != "" {
		in.Prefix = aws.String(p.prefix)
	}
	if p.delim != nil && *p.delim != "" {
		in.Delimiter = p.delim
	}
	if p.maxKeys != nil {
		in.MaxKeys = p.maxKeys
	}
	if p.startAfter != "" && token == "" {
		in.StartAfter = aws.String(p.startAfter)
	}
	if token != "" {
		in.ContinuationToken = aws.String(token)
	}
	return in
}

func (p listParams) v1Input(bucket, token string) *s3.ListObjectsInput {
	in := &s3.ListObjectsInput{Bucket: aws.String(bucket)}
	if p.prefix != "" {
		in.Prefix = aws.String(p.prefix)
	}
	if p.delim != nil && *p.delim != "" {
		in.Delimiter = p.delim
	}
	if p.maxKeys != nil {
		in.MaxKeys = p.maxKeys
	}
	if token != "" {
		in.Marker = aws.String(token)
	}
	return in
}

// entryFilter decides whether a raw listing entry is shown/processed.
type entryFilter struct {
	dirsOnly  bool
	filesOnly bool
	glob      *filter
	prefix    string
	delim     *string
}

func newEntryFilterF(f listFlags, p listParams) entryFilter {
	return entryFilter{
		dirsOnly:  f.dirsOnly,
		filesOnly: f.filesOnly,
		glob:      newFilter(f.include, f.exclude),
		prefix:    p.prefix,
		delim:     p.delim,
	}
}

func (ef entryFilter) visible(e cachedEntry) bool {
	if e.Dir {
		return !ef.filesOnly
	}
	if ef.dirsOnly {
		return false
	}
	// Hide the zero-byte folder-marker object named exactly as a directory
	// prefix (e.g. prefix "logs/" and object "logs/"). Only applies when the
	// prefix itself is a directory-style prefix, so targeting a specific file
	// by its exact key still shows/downloads that file.
	if ef.delim != nil && *ef.delim != "" && ef.prefix != "" &&
		e.Key == ef.prefix && strings.HasSuffix(ef.prefix, *ef.delim) {
		return false
	}
	return ef.glob.Match(relToPrefix(e.Key, ef.prefix))
}

// walkEntries iterates every entry matching the filter, invoking fn for each
// in listing order. Iteration stops early if fn returns cont=false.
//
// When useCache is true and a complete cached snapshot exists for this exact
// listing, entries are served from the cache (instant, fromCache=true);
// otherwise a fresh, full listing is fetched from the server (fromCache=false).
// Used by the export and download paths, which operate on the complete set.
func walkEntries(ctx context.Context, cl *s3x.Client, t *s3x.Target, p listParams, ef entryFilter,
	useCache bool, fn func(e cachedEntry) (cont bool, err error)) (fromCache bool, err error) {
	if useCache {
		cache := loadListingCache()
		fp := listingFingerprint(cl, t, p.prefix, p.delimStr, p.startAfter)
		if e, ok := cache[fp]; ok && e.Complete {
			for _, entry := range e.Entries {
				if !ef.visible(entry) {
					continue
				}
				cont, err := fn(entry)
				if err != nil {
					return true, err
				}
				if !cont {
					return true, nil
				}
			}
			return true, nil
		}
	}
	err = walkEntriesFresh(ctx, cl, t, p, ef, fn)
	return false, err
}

// walkEntriesFresh always lists from the server (no cache read).
func walkEntriesFresh(ctx context.Context, cl *s3x.Client, t *s3x.Target, p listParams, ef entryFilter,
	fn func(e cachedEntry) (cont bool, err error)) error {
	useV1 := false
	token := ""
	first := true
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var (
			pg  *s3x.Page
			err error
		)
		if useV1 {
			pg, err = s3x.ListV1(ctx, cl.S3, p.v1Input(t.Bucket, token))
		} else {
			pg, err = s3x.ListV2(ctx, cl.S3, p.v2Input(t.Bucket, token))
			if err != nil && first && s3x.V2Unsupported(err) {
				useV1 = true
				first = false
				continue
			}
		}
		first = false
		if err != nil {
			return err
		}
		for _, e := range pageToEntries(pg) {
			if !ef.visible(e) {
				continue
			}
			cont, err := fn(e)
			if err != nil {
				return err
			}
			if !cont {
				return nil
			}
		}
		if !pg.Truncated || pg.NextToken == "" {
			return nil
		}
		token = pg.NextToken
	}
}

// listWindow fetches a single paginated listing window starting at token:
// at most limit matching entries, or the complete listing when all is true.
// No cache interaction (safe under concurrent use). It reports the
// continuation token and whether more entries may exist beyond the window.
func listWindow(ctx context.Context, cl *s3x.Client, t *s3x.Target, p listParams, ef entryFilter,
	limit int64, all bool, token string) (entries []cachedEntry, nextToken string, truncated bool, err error) {
	useV1 := false
	first := true
	for {
		if ctx.Err() != nil {
			return nil, "", false, ctx.Err()
		}
		var pg *s3x.Page
		if useV1 {
			marker := token
			if marker == "" {
				marker = t.List.Marker
			}
			pg, err = s3x.ListV1(ctx, cl.S3, p.v1Input(t.Bucket, marker))
		} else {
			pg, err = s3x.ListV2(ctx, cl.S3, p.v2Input(t.Bucket, token))
			if err != nil && first && s3x.V2Unsupported(err) {
				useV1 = true
				first = false
				continue
			}
		}
		first = false
		if err != nil {
			return nil, "", false, err
		}
		pageEntries := pageToEntries(pg)
		visibleLeft := 0
		for _, e := range pageEntries {
			if ef.visible(e) {
				visibleLeft++
			}
		}
		for _, e := range pageEntries {
			if !ef.visible(e) {
				continue
			}
			visibleLeft--
			entries = append(entries, e)
			if !all && int64(len(entries)) >= limit {
				return entries, pg.NextToken, pg.Truncated || visibleLeft > 0, nil
			}
		}
		if !pg.Truncated || pg.NextToken == "" {
			return entries, "", false, nil
		}
		token = pg.NextToken
	}
}
