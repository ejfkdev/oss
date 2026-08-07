package s3x

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Page is one page of listing results. Pages are streamed one at a time so
// buckets with millions of objects never get materialized in memory.
type Page struct {
	Objects   []types.Object
	Prefixes  []string
	Truncated bool
	NextToken string // continuation-token (v2) or marker (v1)
}

// ListV2 performs a single ListObjectsV2 request.
func ListV2(ctx context.Context, c *s3.Client, in *s3.ListObjectsV2Input) (*Page, error) {
	out, err := c.ListObjectsV2(ctx, in)
	if err != nil {
		return nil, err
	}
	p := &Page{Objects: out.Contents, Truncated: aws.ToBool(out.IsTruncated)}
	if out.NextContinuationToken != nil {
		p.NextToken = *out.NextContinuationToken
	}
	for _, cp := range out.CommonPrefixes {
		if cp.Prefix != nil {
			p.Prefixes = append(p.Prefixes, *cp.Prefix)
		}
	}
	return p, nil
}

// ListV1 performs a single legacy ListObjects request (marker based). Some
// old S3 clones do not implement ListObjectsV2.
func ListV1(ctx context.Context, c *s3.Client, in *s3.ListObjectsInput) (*Page, error) {
	out, err := c.ListObjects(ctx, in)
	if err != nil {
		return nil, err
	}
	truncated := aws.ToBool(out.IsTruncated)
	p := &Page{Objects: out.Contents, Truncated: truncated}
	switch {
	case out.NextMarker != nil:
		p.NextToken = *out.NextMarker
	case truncated && len(out.Contents) > 0:
		// Without a delimiter S3 v1 may omit NextMarker; continue after the
		// last key seen.
		p.NextToken = aws.ToString(out.Contents[len(out.Contents)-1].Key)
	}
	for _, cp := range out.CommonPrefixes {
		if cp.Prefix != nil {
			p.Prefixes = append(p.Prefixes, *cp.Prefix)
		}
	}
	return p, nil
}

// V2Unsupported reports whether err indicates that ListObjectsV2 is not
// implemented by the server, so callers can retry with the v1 API.
func V2Unsupported(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "NotImplemented", "XNotImplemented", "OperationNotSupported", "NotSupported":
		return true
	}
	var resp interface{ HTTPStatusCode() int }
	if errors.As(err, &resp) {
		return resp.HTTPStatusCode() == 501
	}
	return false
}
