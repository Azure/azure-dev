// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/adam-lavrik/go-imath/ix"
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
* - When AZD_DEBUG_PROGRESS_LOGS environment variable is set, all logs are written to a file in the current working
*   directory with a name based on the title and timestamp (e.g., "title-20060102-150405.log").
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
	// This function is used to find out what's the terminal width. The log progress is disabled if this function
	// returns a number <= 0.
	terminalWidthFn TerminalWidthFn
	// File handle for debug logging when AZD_DEBUG_PROGRESS_LOGS is set
	debugLogFile *os.File
	// Full content buffer for writing to debug log file
	debugLogBuffer strings.Builder
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
	return newProgressLogWithWidthFn(lines, prefix, title, header, tm.Width)
}

type TerminalWidthFn func() int

// newProgressLogWithWidthFn is a constructor which can inject the implementation for getting the terminal's width.
// This is useful to set and control the progress log width manually.
func newProgressLogWithWidthFn(lines int, prefix, title, header string, widthFn TerminalWidthFn) *progressLog {
	return &progressLog{
		lines:           lines,
		prefix:          prefix,
		title:           title,
		header:          header,
		terminalWidthFn: widthFn,
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

	// Initialize debug logging if AZD_DEBUG_PROGRESS_LOGS is set
	if os.Getenv("AZD_DEBUG_PROGRESS_LOGS") != "" {
		p.initDebugLogFile()
	}

	// title is created on Start() because it depends on terminal width
	// if terminal is resized between stop and start, the previewer will
	// react to it and update the size.
	p.buildTopBottom()

	// header
	if p.header != "" {
		tm.Print(tm.ResetLine(""))
		tm.Println(p.header)
		// margin after title
		tm.Println("")

		// Write header to debug log file
		if p.debugLogFile != nil {
			p.debugLogBuffer.WriteString(p.header)
			p.debugLogBuffer.WriteString("\n\n")
		}
	}

	// title
	tm.Print(tm.ResetLine(""))
	tm.Println(p.displayTitle)
	tm.Println("")

	// Write title to debug log file
	if p.debugLogFile != nil {
		p.debugLogBuffer.WriteString(p.displayTitle)
		p.debugLogBuffer.WriteString("\n\n")
	}

	p.printLogs()
}

// Stop clears the screen from any previous output and clear the buffer.
// If keepLogs is true, the current screen is not cleared.
// Calling Stop() before Start() is a no-op.
func (p *progressLog) Stop(keepLogs bool) {
	if p.output == nil {
		return
	}

	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()

	// Write debug log file if enabled
	if p.debugLogFile != nil {
		p.debugLogBuffer.WriteString("\n")
		p.debugLogBuffer.WriteString(p.footerLine)
		p.debugLogBuffer.WriteString("\n")

		if _, err := p.debugLogFile.WriteString(p.debugLogBuffer.String()); err != nil {
			log.Printf("error writing debug log file: %v", err)
		}

		if err := p.debugLogFile.Close(); err != nil {
			log.Printf("error closing debug log file: %v", err)
		}
		p.debugLogFile = nil
		p.debugLogBuffer.Reset() // Reset buffer for next use
	}

	if !keepLogs {
		p.clearContentAndFlush()

		// title
		tm.MoveCursorUp(2)
		clearLine()

		// header
		if p.header != "" {
			tm.MoveCursorUp(2)
			clearLine()
		}

		tm.Flush()
	}
	p.output = nil
}

// Write implements oi.Writer and updates the internal buffer before flushing it into the screen.
// Calling Write() before Start() or after Stop() is a no-op
func (p *progressLog) Write(logBytes []byte) (int, error) {
	p.outputMutex.Lock()
	defer p.outputMutex.Unlock()

	if p.output == nil {
		return len(logBytes), nil
	}

	// Write to debug log file if enabled (inside mutex for thread safety)
	if p.debugLogFile != nil {
		p.debugLogBuffer.Write(logBytes)
	}

	maxWidth := p.terminalWidthFn()
	if maxWidth <= 0 {
		// maxWidth <= 0 means there's no terminal to write and the stdout pipe is mostly connected to a file or a buffer
		// while azd is been called by another process, like go-test in CI
		return len(logBytes), nil
	}

	// Safety check: ensure output buffer has at least one element
	if len(p.output) == 0 {
		p.output = make([]string, p.lines)
	}

	logsScanner := bufio.NewScanner(strings.NewReader(string(logBytes)))

	var afterFirstLine bool
	for logsScanner.Scan() {
		log := logsScanner.Text()

		// after the list .Scan(), we need to add new line for each new line
		// as .Scan() splits content by new lines, and only the first one can be
		// a continuation to current line
		if afterFirstLine {
			p.output = append(p.output[1:], p.prefix)
		} else {
			afterFirstLine = true
		}

		fullLog := log
		if p.output[len(p.output)-1] == "" {
			fullLog = p.prefix + log
		}
		fullLogLen := len(fullLog)

		for fullLogLen > 0 {
			// Get whatever is the empty space on current line
			currentLineRemaining := maxWidth - len(p.output[len(p.output)-1])
			if currentLineRemaining == 0 {
				// line is full, use next line. Add prefix first
				p.output = append(p.output[1:], p.prefix)
				currentLineRemaining = maxWidth - len(p.prefix)
			}

			// Choose between writing fullLog (if it is less than currentLineRemaining)
			// or writing only currentLineRemaining
			writeLen := ix.Min(fullLogLen, currentLineRemaining)
			p.output[len(p.output)-1] += fullLog[:writeLen]
			fullLog = fullLog[writeLen:]
			fullLogLen = len(fullLog)
		}
		p.clearContentAndFlush()
		p.printLogs()
	}

	if err := logsScanner.Err(); err != nil {
		log.Printf("error while reading logs for previewer: %v", err)
	}

	// .Scan() won't add a line break for a line which ends in `\n`
	// This is because the next Scan() after \n will find EOF.
	// Adding a line break for such case.
	if logBytes[len(logBytes)-1] == '\n' {
		p.output = append(p.output[1:], p.prefix)
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

	oldHeader := p.header
	p.header = header

	p.clearContentAndFlush()
	if p.header != "" {
		tm.MoveCursorUp(4)
		clearLine()
		tm.Println(p.header)
		tm.MoveCursorDown(3)

		// Update debug log file if enabled
		if p.debugLogFile != nil {
			// Replace old header in buffer with new header
			bufferContent := p.debugLogBuffer.String()
			if oldHeader != "" {
				bufferContent = strings.Replace(bufferContent, oldHeader+"\n\n", p.header+"\n\n", 1)
			} else {
				// If there was no old header, prepend the new header
				bufferContent = p.header + "\n\n" + bufferContent
			}
			p.debugLogBuffer.Reset()
			p.debugLogBuffer.WriteString(bufferContent)
		}
	}
	p.printLogs()
}

/****************** Not exported method ****************/

// initDebugLogFile creates a debug log file if AZD_DEBUG_PROGRESS_LOGS environment variable is set.
// The file is created in the current working directory with a unique name based on the title and timestamp.
func (p *progressLog) initDebugLogFile() {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("error getting current directory for debug log: %v", err)
		return
	}

	// Create a safe filename from the title
	safeTitle := p.sanitizeFilename(p.title)
	if safeTitle == "" {
		safeTitle = "progress"
	}

	// Generate unique filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s.log", safeTitle, timestamp)
	filePath := filepath.Join(cwd, filename)

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		log.Printf("error creating debug log file %s: %v", filePath, err)
		return
	}

	p.debugLogFile = file
	log.Printf("Debug progress logs will be written to: %s", filePath)
}

// sanitizeFilename converts a string into a safe filename by removing or replacing invalid characters.
func (p *progressLog) sanitizeFilename(s string) string {
	// Replace spaces with underscores
	s = strings.ReplaceAll(s, " ", "_")

	// Remove or replace invalid filename characters
	invalidChars := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	s = invalidChars.ReplaceAllString(s, "")

	// Limit length to avoid filesystem issues
	if len(s) > 100 {
		s = s[:100]
	}

	// Remove leading/trailing dots and spaces
	s = strings.Trim(s, ". ")

	return s
}

// clearLine override text with empty spaces.
func clearLine() {
	tm.Print(tm.ResetLine(""))
}

// clearContentAndFlush removes the current logs from the screen.
func (p *progressLog) clearContentAndFlush() {
	if p.output == nil {
		return
	}

	// footer
	clearLine()
	tm.MoveCursorUp(1)

	// output
	for range p.output {
		tm.MoveCursorUp(1)
		clearLine()
	}

	tm.Flush()
}

// printLogs write the content from the buffer as logs.
func (p *progressLog) printLogs() {
	for index := range p.output {
		tm.Print(tm.ResetLine(""))
		tm.Println(p.output[index])
	}

	// margin after output
	tm.Println("")
	tm.Print(p.footerLine)

	tm.Flush()
}

// buildTopBottom creates the display title and frames during initialization.
func (p *progressLog) buildTopBottom() {
	consoleLen := p.terminalWidthFn()
	withPrefixTitle := p.prefix + p.title
	titleLen := len(withPrefixTitle)

	if consoleLen <= 0 {
		// consoleLen <= 0 means a CI terminal, where logs can be just written
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
		p.displayTitle = withPrefixTitle[:consoleLen-4] + truncationDots
		return
	}

	// Guaranteed titleLen < consoleLen at this point
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
