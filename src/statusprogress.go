package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
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
}

var statusProgressFunc = newStatusProgress

func newStatusProgress(output io.Writer, color bool) *statusProgress {
	file, ok := output.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) || os.Getenv("TERM") == "dumb" {
		return nil
	}
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil || width < 20 || height < 5 {
		return nil
	}
	return &statusProgress{output: output, color: color, width: width, height: height}
}

func (p *statusProgress) start() error {
	p.done, p.stopped = make(chan struct{}), make(chan struct{})
	p.active = "Inspecting local Git state"
	p.write("\x1b[?25l")
	p.paint()
	err := p.err
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
	p.paint()
}

func (p *statusProgress) close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		close(p.done)
		<-p.stopped
		p.mu.Lock()
		defer p.mu.Unlock()
		p.clear()
		if commandContext.Err() != nil {
			p.write(p.frame)
		}
		// Attempt restoration even if a previous write failed.
		_, err := io.WriteString(p.output, "\x1b[?25h")
		if p.err == nil {
			p.err = err
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
	if p.lines > 1 {
		p.write(fmt.Sprintf("\x1b[%dA", p.lines-1))
	}
	p.write("\r\x1b[J")
	p.lines = 0
}

func (p *statusProgress) paint() {
	p.clear()
	lines := statusProgressLines(p.frame, p.width, p.height-2)
	spinner := string("|/-\\"[p.step%4]) + " " + p.active
	if p.color {
		spinner = "\x1b[36m" + spinner + "\x1b[0m"
	}
	lines = append(lines, ansi.Truncate(spinner, p.width-1, "…"))
	p.write(strings.Join(lines, "\n"))
	p.lines = len(lines)
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
