package app

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schollz/progressbar/v3"
)

// newBar creates a determinate progress bar for single-file transfers.
func newBar(size int64, desc string) *progressbar.ProgressBar {
	return progressbar.NewOptions64(size,
		progressbar.OptionSetDescription(desc),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowBytes(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionFullWidth(),
		progressbar.OptionOnCompletion(func() { fmt.Fprintln(os.Stderr) }),
	)
}

// progressWriter feeds a progress bar from concurrent multipart writes.
type progressWriter struct {
	w interface {
		WriteAt([]byte, int64) (int, error)
	}
	bar *progressbar.ProgressBar
}

func (p progressWriter) WriteAt(b []byte, off int64) (int, error) {
	n, err := p.w.WriteAt(b, off)
	if n > 0 {
		_ = p.bar.Add(n)
	}
	return n, err
}

// aggregate renders a one-line running counter for recursive transfers,
// where the total is unknown up front (listing is streamed, not buffered).
type aggregate struct {
	enabled bool
	icon    string
	files   atomic.Int64
	bytes   atomic.Int64
	skipped atomic.Int64
	start   time.Time
	done    chan struct{}
	once    sync.Once
}

func newAggregate(enabled bool, icon string) *aggregate {
	a := &aggregate{enabled: enabled, icon: icon, start: time.Now(), done: make(chan struct{})}
	if enabled {
		go a.loop()
	}
	return a
}

func (a *aggregate) loop() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-t.C:
			a.render()
		}
	}
}

func (a *aggregate) render() {
	b := a.bytes.Load()
	secs := time.Since(a.start).Seconds()
	rate := 0.0
	if secs > 0 {
		rate = float64(b) / secs
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s %s %s, %s (%s/s)",
		eCyan(a.icon), eBold(fmt.Sprintf("%d", a.files.Load())), T("个文件", "files"),
		humanSize(b, false), humanSize(int64(rate), false))
}

func (a *aggregate) Add(files, bytes int64) {
	a.files.Add(files)
	a.bytes.Add(bytes)
}

func (a *aggregate) Skip() { a.skipped.Add(1) }

// Finish stops the ticker and prints the final summary line.
func (a *aggregate) Finish() {
	a.once.Do(func() {
		close(a.done)
		if a.enabled {
			a.render()
			fmt.Fprintln(os.Stderr)
		}
		extra := ""
		if sk := a.skipped.Load(); sk > 0 {
			extra = fmt.Sprintf(T("，跳过 %d", ", skipped %d"), sk)
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", checkMark(), eGreen(fmt.Sprintf(
			T("完成: %d 个文件，%s，耗时 %s%s", "done: %d files, %s in %s%s"),
			a.files.Load(), humanSize(a.bytes.Load(), false),
			time.Since(a.start).Round(time.Millisecond), extra)))
	})
}
