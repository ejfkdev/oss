package app

import (
	"strconv"
	"time"

	"github.com/dustin/go-humanize"
)

func humanSize(n int64, raw bool) string {
	if raw {
		return strconv.FormatInt(n, 10)
	}
	return humanize.IBytes(uint64(n))
}

func humanTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
