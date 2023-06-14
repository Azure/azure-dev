// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"strings"
	"sync"

	tm "github.com/buger/goterm"
)

/* progressLog implements the io.Writer interface and writes bytes applying a framed style like
*
************************************************
*  <header>
*
*  <displayTitle>
*
*  <prefix><logA
*  <prefix><logB>
*  <prefix><logC>
*  <prefix><logD>
*  <prefix><logN...>
*
*  ───────────────────────────────────────────
*************************************************
*
* Notes:
* - prefix can be used for setting indentation for the output.
* - The line size for the title and the footer are calculated during Start(), filling the available space from the screen.
* - <displayTitle> is generated as `<prefix>──── <title> ──────────────` where:
*    - lines on the left and right of the title are calculated based on the available space on screen minus the prefix and
*      title length. The left line represent 1/10 of the entire line, leaving the rest for the right line.
*    - The less space on the screen, the shorter the lines will be. Lines won't be displayed if there is not enough room.
* - The title and the logs are truncated with the symbol `...` at the end when it is bigger than the screen width.
 */
type progressLog struct {
	// The initial top wording. This value can be updated using Header() method.
	header string
	// The raw initial title for the component. This value can't be changed after Starting the component.
	title string
	// The title that is displayed on the form of `<prefix>──── <title> ──────────────`.
	displayTitle string
	// The line on the bottom. The value is saved on the component state so it is not generated on every re-draw.
	footerLine string
	// The number of rows to display for logging.
	lines int
	// Use prefix for indentation or any other symbol before each line.
	prefix string
	// This list is used as the memory buffer for the logs. The buffer is kept with the size of `lines`
	output []string
	// The mutex is used to coordinate updating the header, stopping the component and printing logs.
	outputMutex sync.Mutex
}

/****************** Exported method ****************
- NewProgressLog
- Start
- Stop
- Write
- Header
*/

// NewProgressLog returns a new instance of a progressLog.
func NewProgressLog(lines int, prefix, title, header string) *progressLog {
	return &progressLog{
		lines:  lines,
		prefix: prefix,
		title:  title,
		header: header,
	}
}

// Start must be call to initialize the component and draw the initial empty frame in the screen.
// Calling Start() a second time is a no-op. The screen is only updated during the first call.
// Call Stop() to remove the frame and logs from the screen.
func (p *progressLog) Start() {
	if p.output != nil {
		return
	}
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

	p.printLogs()
}

// Stop clears the screen from any previous output and clear the buffer.
// Calling Stop() before Start() is a no-op.
func (p *progressLog) Stop() {
	if p.output == nil {
		return
	}

	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()

	p.clearContentAndFlush()

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

// Write implements oi.Writer and updates the internal buffer before flushing it into the screen.
// Calling Write() before Start() or after Stop() is a no-op
func (p *progressLog) Write(logBytes []byte) (int, error) {
	if p.output == nil {
		return len(logBytes), nil
	}

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

		p.clearContentAndFlush()
		p.output = append(p.output[1:], fullLog)
		p.printLogs()
	}
	return len(logBytes), nil
}

// Header updates the previewer's top header
// Calling Header() before Start() or after Stop() is a no-op
func (p *progressLog) Header(header string) {
	if p.output == nil {
		return
	}

	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()
	p.header = header

	p.clearContentAndFlush()
	if p.header != "" {
		tm.MoveCursorUp(4)
		clearLine(p.header)
		tm.Println(p.header)
		tm.MoveCursorDown(3)
	}
	p.printLogs()
}

/****************** Not exported method ****************/

// clearLine override text with empty spaces.
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

// clearContentAndFlush removes the current logs from the screen.
func (p *progressLog) clearContentAndFlush() {
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

// printLogs write the content from the buffer as logs.
func (p *progressLog) printLogs() {
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

// buildTitleMargins creates the display title and frames during initialization.
func (p *progressLog) buildTitleMargins() {
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
