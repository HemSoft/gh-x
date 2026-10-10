package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Only terminal presentation is concurrent. Acquisition, cache publication and
// section rendering remain serial; the animator reads immutable rendered text.
type statusProgress struct {
	mu        sync.Mutex
	output    io.Writer
	color     bool
	width     int
	height    int
	frame     string
	active    string
	lines     int
	step      int
	err       error
	ready     [4]bool
	done      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
	size      func() (int, int, error)
	drawn     string
	restoreVT func() error
}

var statusProgressFunc = newStatusProgress
var statusTerminalSizeFunc = term.GetSize
var statusVirtualTerminalFunc = func(output io.Writer) (func() error, error) {
	return termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(output))
}

func newStatusProgress(output io.Writer, color bool) *statusProgress {
	file, ok := output.(*os.File)
	if !ok || !terminalOutputFunc(output) || os.Getenv("TERM") == "dumb" {
		return nil
	}
	getSize := statusTerminalSizeFunc
	size := func() (int, int, error) { return getSize(int(file.Fd())) }
	width, height, err := size()
	if err != nil || width < 20 || height < 5 {
		return nil
	}
	restore, err := statusVirtualTerminalFunc(output)
	if err != nil {
		return nil
	}
	return &statusProgress{output: output, color: color, width: width, height: height, size: size, restoreVT: restore}
}

func (p *statusProgress) start() error {
	p.done, p.stopped = make(chan struct{}), make(chan struct{})
	p.active = "Inspecting local Git state"
	p.write("\x1b[?25l")
	p.paint()
	err := p.err
	notifiedMu.Lock()
	accountProgress = p
	notifiedMu.Unlock()
	go p.animate()
	return err
}

func (p *statusProgress) animate() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer close(p.stopped)
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

func (p *statusProgress) tick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.step++
	if p.refreshSize() {
		p.paint()
		return
	}
	if p.width < 2 || p.height < 3 {
		return
	}
	p.write("\r\x1b[2K" + p.spinner())
}

func (p *statusProgress) close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		close(p.done)
		<-p.stopped
		notifiedMu.Lock()
		defer notifiedMu.Unlock()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.refreshSize()
		p.clear()
		if commandContext.Err() != nil {
			p.write(p.frame)
		}
		// Attempt restoration even if a previous write failed.
		_, err := io.WriteString(p.output, "\x1b[?25h")
		if p.err == nil {
			p.err = err
		}
		if p.restoreVT != nil {
			p.err = errors.Join(p.err, p.restoreVT())
		}
		if accountProgress == p {
			accountProgress = nil
		}
	})
	return p.err
}

func (p *statusProgress) write(text string) {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.output, text)
}

func (p *statusProgress) clear() {
	p.write(statusProgressClear(p.lines))
	p.lines = 0
	p.drawn = ""
}

func (p *statusProgress) paint() {
	p.refreshSize()
	if p.width < 2 || p.height < 3 {
		p.clear()
		return
	}
	lines := statusProgressLines(p.frame, p.width, p.height-2)
	lines = append(lines, p.spinner())
	// One write prevents a blank frame between clearing and repainting.
	p.drawn = strings.Join(lines, "\n")
	p.write(statusProgressClear(p.lines) + p.drawn)
	p.lines = len(lines)
}

func (p *statusProgress) refreshSize() bool {
	if p.size == nil {
		return false
	}
	width, height, err := p.size()
	if err != nil || width <= 0 || height <= 0 || (width == p.width && height == p.height) {
		return false
	}
	p.lines = statusProgressRows(p.drawn, width)
	p.width, p.height = width, height
	return true
}

func statusProgressRows(frame string, width int) int {
	if frame == "" {
		return 0
	}
	rows := 0
	for _, line := range strings.Split(frame, "\n") {
		rows += max(1, (ansi.StringWidth(line)+width-1)/width)
	}
	return rows
}

// noteFallback and close both acquire notifiedMu before the presentation lock.
// The permanent stderr notice sits above the newly anchored live preview.
func (p *statusProgress) notice(writer io.Writer, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshSize()
	p.clear()
	fmt.Fprint(writer, text)
	p.paint()
}

func statusProgressClear(lines int) string {
	prefix := ""
	if lines > 1 {
		prefix = fmt.Sprintf("\x1b[%dA", lines-1)
	}
	return prefix + "\r\x1b[J"
}

func (p *statusProgress) spinner() string {
	spinner := string("|/-\\"[p.step%4]) + " " + p.active
	if p.color {
		spinner = "\x1b[36m" + spinner + "\x1b[0m"
	}
	return ansi.Truncate(spinner, p.width-1, "…")
}

func statusProgressLines(frame string, width, limit int) []string {
	if frame == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	if len(lines) > limit {
		header := min(7, limit/2)
		tail := limit - header - 1
		note := fmt.Sprintf("… %d earlier lines; full output follows", len(lines)-header-tail)
		lines = append(append(lines[:header:header], note), lines[len(lines)-tail:]...)
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width-1, "…")
	}
	return lines
}

func (p *statusProgress) begin(active string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = active
	p.paint()
}

func (p *statusProgress) local(dashboard *statusDashboard) {
	if p == nil {
		return
	}
	p.begin("Checking cached GitHub data")
	p.update(dashboard)
}

func (p *statusProgress) cached(dashboard *statusDashboard, now time.Time) {
	if p == nil {
		return
	}
	for i, section := range dashboard.remoteSections {
		p.ready[i] = now.Before(section.RetryAfter)
	}
	p.update(dashboard)
}

func completeStatusSection(dashboard *statusDashboard, section int) {
	markStatusSectionFetch(dashboard, section, statusNowFunc())
	if p := dashboard.progress; p != nil {
		p.ready[section] = true
		p.update(dashboard)
	}
}

func (p *statusProgress) update(dashboard *statusDashboard) {
	var frame bytes.Buffer
	styler := newTableStyler(&frame, p.color)
	renderStatusHeader(&frame, styler, *dashboard)
	renderers := []func(io.Writer, tableStyler, statusDashboard) error{
		renderStatusIssueSection, renderStatusPullRequestSection,
		renderStatusMergedPullRequestSection, renderStatusWorkflowRunSection,
	}
	var err error
	for i, render := range renderers {
		if p.ready[i] {
			err = render(&frame, styler, *dashboard)
			if err != nil {
				break
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
	p.frame = frame.String()
	p.paint()
}
