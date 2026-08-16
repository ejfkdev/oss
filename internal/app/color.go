package app

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/mattn/go-isatty"
)

// Output coloring: enabled only for interactive terminals. When output is
// piped to another program (or NO_COLOR is set), plain text is emitted so
// downstream parsing is never broken.

type colorMode int

const (
	colorAuto colorMode = iota
	colorAlways
	colorNever
)

var (
	colorMu sync.Mutex
	// colorPref is set from the --color flag; auto until then.
	colorPref = colorAuto
)

func setColorPreference(pref colorMode) {
	colorMu.Lock()
	colorPref = pref
	colorMu.Unlock()
}

func envForbidsColor() bool {
	return os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
}

func colorEnabledFor(f *os.File) bool {
	colorMu.Lock()
	pref := colorPref
	colorMu.Unlock()
	switch pref {
	case colorAlways:
		return true
	case colorNever:
		return false
	default:
		if envForbidsColor() {
			return false
		}
		return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
}

// colorEnabled reports whether stdout should be colored.
func colorEnabled() bool { return colorEnabledFor(os.Stdout) }

// stderrColor reports whether stderr messages should be colored.
func stderrColor() bool { return colorEnabledFor(os.Stderr) }

func paint(s, code string) string {
	if s == "" || !colorEnabled() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// paintErr colors messages destined for stderr.
func paintErr(s, code string) string {
	if s == "" || !stderrColor() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func cBold(s string) string   { return paint(s, "1") }
func cDim(s string) string    { return paint(s, "2") }
func cBlue(s string) string   { return paint(s, "34") }
func cCyan(s string) string   { return paint(s, "36") }
func cGreen(s string) string  { return paint(s, "32") }
func cYellow(s string) string { return paint(s, "33") }
func cRed(s string) string    { return paint(s, "31") }

// cGrey renders dim grey (bright black) for de-emphasized text.
func cGrey(s string) string { return paint(s, "90") }

// cGreenBright renders bold bright green — used to make anonymously
// listable buckets stand out in `oss find` output.
func cGreenBright(s string) string { return paint(s, "1;92") }

func eYellow(s string) string { return paintErr(s, "33") }
func eGreen(s string) string  { return paintErr(s, "32") }
func eCyan(s string) string   { return paintErr(s, "36") }
func eBold(s string) string   { return paintErr(s, "1") }

// dirKey styles a directory entry key (bold blue, like ls(1) directories).
func dirKey(s string) string { return paint(s, "1;34") }

// sizeColored colors a human-readable size by magnitude:
// dim (<1 KiB), green (<1 MiB), yellow (<1 GiB), red (>=1 GiB).
// IMPORTANT: pad the string BEFORE calling this; coloring after padding
// keeps fixed-width column alignment intact.
func sizeColored(human string, n int64) string {
	switch {
	case n < 1<<10:
		return paint(human, "2")
	case n < 1<<20:
		return paint(human, "32")
	case n < 1<<30:
		return paint(human, "33")
	default:
		return paint(human, "31")
	}
}

// checkMark returns a green check mark for stderr success lines when colors
// are enabled, and an empty string otherwise (plain output stays unadorned).
func checkMark() string {
	if stderrColor() {
		return paintErr("✓", "32") + " "
	}
	return ""
}

// PrintError renders a fatal error to stderr (red when interactive).
func PrintError(err error) {
	if stderrColor() {
		fmt.Fprintf(os.Stderr, "\x1b[1;31moss:\x1b[0m \x1b[31m%v\x1b[0m\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "oss: %v\n", err)
}

// displayWidth counts terminal columns: CJK ideographs take 2 cells.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			w += 2
		} else {
			w += 1
		}
	}
	return w
}

// padDisplay pads s with spaces up to width terminal columns (CJK-aware).
// Call BEFORE coloring so ANSI escapes don't disturb the width calculation.
func padDisplay(s string, width int) string {
	if w := displayWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
