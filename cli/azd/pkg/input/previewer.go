// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"strings"

	tm "github.com/buger/goterm"
)

type previewer struct {
	title  string
	lines  int
	prefix string
	output []string
}

func NewPreviewer(lines int, prefix string) *previewer {
	return &previewer{
		lines:  lines,
		output: make([]string, lines),
		prefix: prefix,
	}
}

func (p *previewer) SetTitle(title string) {
	p.title = title
}

func (p *previewer) Start() {
	printOutput(p.output, p.title)
}

func clearString(text string) {
	sizeLog := len(text)
	eraseWith := strings.Repeat(" ", sizeLog)
	tm.MoveCursorUp(1)
	tm.Printf(eraseWith)
	tm.MoveCursorBackward(sizeLog)
}

func (p *previewer) Stop() {
	for _, log := range p.output {
		clearString(log)
	}
	if p.title != "" {
		clearString(p.title)
	}
	tm.Flush()
}

func (p *previewer) Write(logBytes []byte) (int, error) {
	lastChar := len(logBytes) - 1
	if logBytes[lastChar] == '\n' {
		logBytes = logBytes[:lastChar]
	}
	log := string(logBytes)
	fullLog := p.prefix + log
	maxWidth := tm.Width()

	if maxWidth <= 0 {
		// tm.Width <= 0 means a CI terminal, where logs can be just written
		return tm.Println(fullLog)
	}

	if len(fullLog) > maxWidth {
		fullLog = fullLog[:maxWidth-4] + cPostfix
	}

	p.Stop()
	p.output = pushRemove(p.output, fullLog)
	printOutput(p.output, p.title)
	return len(logBytes), nil
}

func pushRemove(original []string, value string) []string {
	copy := original[1:] // remove first
	return append(copy, value)
}

func printOutput(output []string, title string) {
	size := len(output)
	count := size
	if title != "" {
		tm.ResetLine("")
		tm.Println(title)
	}
	for count > 0 {
		count--
		tm.ResetLine("")
		tm.Println(output[count])
	}
	tm.Flush()
}
