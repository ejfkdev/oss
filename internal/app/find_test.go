package app

import (
	"strings"
	"testing"

	"github.com/ejfkdev/oss/internal/s3x"
)

func TestProbeURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://b.s3.amazonaws.com/", "https://b.s3.amazonaws.com/?max-keys=1"},
		{"https://b.s3.amazonaws.com/?x=1", "https://b.s3.amazonaws.com/?x=1&max-keys=1"},
	}
	for _, c := range cases {
		if got := probeURL(c.in); got != c.want {
			t.Errorf("probeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildFindJobsBareName(t *testing.T) {
	jobs, invalid := buildFindJobs([]string{"mybucket"}, &s3x.ConnOpts{}, "")
	if len(invalid) != 0 {
		t.Fatalf("unexpected invalid: %v", invalid)
	}
	want := 0
	for _, p := range s3x.ScanProbes {
		if len(p.Regions) == 0 {
			want++
		} else {
			want += len(p.Regions)
		}
	}
	if len(jobs) != want {
		t.Fatalf("bare name should fan out to %d probes, got %d", want, len(jobs))
	}
	for _, j := range jobs {
		if j.Input != "mybucket" {
			t.Errorf("input = %q, want mybucket", j.Input)
		}
		if !strings.Contains(j.URL, "mybucket") {
			t.Errorf("URL %q should contain the bucket", j.URL)
		}
	}
}

func TestBuildFindJobsFullURL(t *testing.T) {
	jobs, invalid := buildFindJobs([]string{"https://mybucket.s3.us-east-1.amazonaws.com/prefix"}, &s3x.ConnOpts{}, "")
	if len(invalid) != 0 {
		t.Fatalf("unexpected invalid: %v", invalid)
	}
	if len(jobs) != 1 {
		t.Fatalf("full URL should produce 1 probe, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Provider != "aws" {
		t.Errorf("provider = %q, want aws", j.Provider)
	}
	if j.URL != "https://mybucket.s3.us-east-1.amazonaws.com/" {
		t.Errorf("URL = %q", j.URL)
	}
	if j.Region != "us-east-1" {
		t.Errorf("region = %q", j.Region)
	}
}

func TestBuildFindJobsInvalid(t *testing.T) {
	jobs, invalid := buildFindJobs([]string{"!!bad!!"}, &s3x.ConnOpts{}, "")
	if len(jobs) != 0 {
		t.Fatalf("invalid name should produce no jobs, got %d", len(jobs))
	}
	if _, ok := invalid["!!bad!!"]; !ok {
		t.Errorf("expected !!bad!! to be marked invalid")
	}
}

func TestBuildExportRowsListableField(t *testing.T) {
	results := []findResult{
		{Input: "a", Provider: "aws", Name: "AWS S3", State: findListable, URL: "https://a.s3.amazonaws.com/", Listable: true},
		{Input: "b", Provider: "aliyun", Name: "Aliyun OSS", State: findExists, URL: "https://b.oss-cn-hangzhou.aliyuncs.com/", Listable: false},
		{Input: "c", Provider: "aws", Name: "AWS S3", State: findNotFound, URL: "https://c.s3.amazonaws.com/", Listable: false},
	}
	rows := buildExportRows([]string{"a", "b", "c"}, results, map[string]string{})
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	// Sorted: listable first.
	if rows[0].State != findListable {
		t.Errorf("first row should be listable, got %q", rows[0].State)
	}
	// listable_url filled only for the listable one.
	for _, r := range rows {
		if r.State == findListable {
			if r.ListableURL != r.URL {
				t.Errorf("listable row should have listable_url = url, got %q", r.ListableURL)
			}
		} else if r.ListableURL != "" {
			t.Errorf("non-listable row should have empty listable_url, got %q", r.ListableURL)
		}
	}
}

func TestStateRank(t *testing.T) {
	order := []string{findListable, findExists, findUnknown, findNotFound, "invalid"}
	for i := 0; i+1 < len(order); i++ {
		if stateRank(order[i]) >= stateRank(order[i+1]) {
			t.Errorf("stateRank(%q) should be < stateRank(%q)", order[i], order[i+1])
		}
	}
}
