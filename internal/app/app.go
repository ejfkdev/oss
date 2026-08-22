package app

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// RepoURL is the project homepage shown in help and version output.
const RepoURL = "https://github.com/ejfkdev/oss"

// apiVersion is the running version, exposed for the MCP server identity.
var apiVersion = "dev"

// renderRows renders "key -> description" rows with the key column padded to
// a common width (counted in runes; keys are ASCII so this aligns correctly)
// and optionally colored.
func renderRows(rows [][2]string, keyColor func(string) string, indent string) []string {
	maxw := 0
	for _, r := range rows {
		if w := utf8.RuneCountInString(r[0]); w > maxw {
			maxw = w
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		pad := maxw - utf8.RuneCountInString(r[0])
		k := r[0]
		if keyColor != nil {
			k = keyColor(k)
		}
		out = append(out, indent+k+strings.Repeat(" ", pad)+"  "+r[1])
	}
	return out
}

func printSection(b io.Writer, title string, lines []string) {
	fmt.Fprintf(b, "\n%s:\n", cBold(title))
	for _, l := range lines {
		fmt.Fprintln(b, l)
	}
}
