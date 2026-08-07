package app

import (
	"context"
	"math"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"

	"oss/internal/s3x"
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

func resolveListParams(c *cli.Command, t *s3x.Target) listParams {
	// Delimiter resolution: -r > --delimiter flag > URL ?delimiter= > "/".
	var delim *string
	switch {
	case c.Bool("recursive"):
	case c.IsSet("delimiter"):
		v := c.String("delimiter")
		delim = &v
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

	prefix := t.Key + t.List.Prefix + c.String("prefix")

	var maxKeys *int32
	if c.IsSet("page-size") && c.Int64("page-size") > 0 {
		v := int32(min(c.Int64("page-size"), math.MaxInt32))
		maxKeys = &v
	} else if t.List.MaxKeys > 0 {
		maxKeys = &t.List.MaxKeys
	}

	startAfter := c.String("start-after")
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

func newEntryFilter(c *cli.Command, p listParams) entryFilter {
	return entryFilter{
		dirsOnly:  c.Bool("dirs"),
		filesOnly: c.Bool("files"),
		glob:      newFilter(c.StringSlice("include"), c.StringSlice("exclude")),
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
