package app

import "path"

// filter applies --include/--exclude glob filters to relative object paths.
type filter struct {
	includes []string
	excludes []string
}

func newFilter(includes, excludes []string) *filter {
	return &filter{includes: includes, excludes: excludes}
}

// Match reports whether a relative path passes the filters. Patterns are
// matched against both the full relative path and its base name, so "*.log"
// works at any depth.
func (f *filter) Match(rel string) bool {
	if len(f.includes) > 0 && !anyGlob(f.includes, rel) {
		return false
	}
	if len(f.excludes) > 0 && anyGlob(f.excludes, rel) {
		return false
	}
	return true
}

func anyGlob(patterns []string, rel string) bool {
	base := path.Base(rel)
	for _, p := range patterns {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}
