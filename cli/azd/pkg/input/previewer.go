// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"strings"
	"sync"

	tm "github.com/buger/goterm"
)

type previewer struct {
	title        string
	displayTitle string
	footerLine   string
	header       string
	lines        int
	prefix       string
	output       []string
	outputMutex  sync.Mutex
}

func NewPreviewer(lines int, prefix, title, header string) *previewer {
	return &previewer{
		lines:  lines,
		prefix: prefix,
		title:  title,
		header: header,
	}
}

func (p *previewer) Start() {
	p.output = make([]string, p.lines)
	// title is created on Start() because it depends on terminal width
	// if terminal is resized between stop and start, the previewer will
	// react to it and update the size.
	p.buildTitleMargins()

	// header
	if p.header != "" {
		tm.ResetLine("")
		tm.Println(p.header)
		// margin after title
		tm.Println("")
	}

	// title
	tm.ResetLine("")
	tm.Println(p.displayTitle)
	tm.Println("")

	p.printOutput()
}

func (p *previewer) Stop() {
	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()

	p.clear()

	// title
	tm.MoveCursorUp(2)
	clearLine(p.displayTitle)

	// header
	if p.header != "" {
		tm.MoveCursorUp(2)
		clearLine(p.header)
	}

	tm.Flush()
	p.output = nil
}

func (p *previewer) Write(logBytes []byte) (int, error) {
	fullText := strings.Split(string(logBytes), "\n")
	maxWidth := tm.Width()
	if maxWidth <= 0 {
		// tm.Width <= 0 means there's no terminal to write and the stdout pipe is mostly connected to a file or a buffer
		// while azd is been called by another process, like go-test in CI
		return len(logBytes), nil
	}

	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()

	for _, log := range fullText {
		if log == "" {
			continue
		}
		fullLog := p.prefix + log
		if len(fullLog) > maxWidth {
			fullLog = fullLog[:maxWidth-4] + cPostfix
		}

		p.clear()
		p.output = pushRemove(p.output, fullLog)
		p.printOutput()
	}
	return len(logBytes), nil
}

// Header updates the previewer's top header
func (p *previewer) Header(header string) {
	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()
	p.header = header

	p.clear()
	tm.MoveCursorUp(2)
	clearLine(p.displayTitle)
	if p.header != "" {
		tm.MoveCursorUp(2)
		clearLine(p.header)
	}

	if p.header != "" {
		tm.ResetLine("")
		tm.Println(p.header)
		// margin after title
		tm.Println("")
	}
	// title
	tm.ResetLine("")
	tm.Println(p.displayTitle)
	tm.Println("")

	p.printOutput()
}

func clearLine(text string) {
	sizeLog := len(text)
	if sizeLog == 0 {
		return
	}
	maxWidth := tm.Width()
	if sizeLog > maxWidth {
		sizeLog = maxWidth
	}
	eraseWith := ""
	if sizeLog > 0 {
		eraseWith = strings.Repeat(" ", sizeLog)
	}
	tm.Printf(eraseWith)
	tm.MoveCursorBackward(sizeLog)
}

func (p *previewer) clear() {
	if p.output == nil {
		return
	}

	// footer
	tm.MoveCursorBackward(len(p.footerLine))
	clearLine(p.footerLine)
	tm.MoveCursorUp(1)

	// output
	size := len(p.output) - 1
	for index := range p.output {
		tm.MoveCursorUp(1)
		clearLine(p.output[size-index])
	}

	tm.Flush()
}

func pushRemove(original []string, value string) []string {
	copy := original[1:] // remove first
	return append(copy, value)
}

func (p *previewer) printOutput() {
	size := len(p.output)
	count := 0

	for count < size {
		tm.ResetLine("")
		tm.Println(p.output[count])
		count++
	}

	// margin after output
	tm.Println("")
	tm.Print(p.footerLine)

	tm.Flush()
}

func (p *previewer) buildTitleMargins() {
	consoleLen := tm.Width()
	withPrefixTitle := p.prefix + p.title
	titleLen := len(withPrefixTitle)

	if consoleLen <= 0 {
		// tm.Width <= 0 means a CI terminal, where logs can be just written
		// no lines required.
		p.displayTitle = withPrefixTitle
		// no need for end line
		p.footerLine = ""
		return
	}

	// end line is all space after the prefix
	p.footerLine = p.prefix + strings.Repeat("─", consoleLen-len(p.prefix))

	if titleLen >= consoleLen {
		// can't add lines as title is longer than what's available
		// limit output to what's available
		p.displayTitle = withPrefixTitle[:consoleLen-4] + cPostfix
		return
	}

	// Note that titleLen is > than consoleLen at this point
	remainingSpace := consoleLen - titleLen

	if p.title == "" {
		// using single line for remaining space as title is empty
		p.displayTitle = p.prefix + strings.Repeat("─", remainingSpace)
		return
	}

	if remainingSpace <= 2 {
		// there's either 1 or 2 spaces left.
		// Since we need 2 spaces to add a margin for the title, like:
		// <prefix>── title ─────
		// We can't add lines.
		p.displayTitle = withPrefixTitle
		return
	}

	// Note tat remainingSpace is > than 2 at this point,
	// it is safe to remove two spaces from it to add margin
	remainingSpace -= 2

	// the line from the left will be 1/6 from what's available
	left := remainingSpace / 10
	right := remainingSpace - left

	p.displayTitle = p.prefix + strings.Repeat("─", left) + " " + p.title + " " + strings.Repeat("─", right)
}
