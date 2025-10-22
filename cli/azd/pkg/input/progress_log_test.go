// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/bradleyjkemp/cupaloy/v2"
	tm "github.com/buger/goterm"
	"github.com/stretchr/testify/require"
)

const prefix = "`prefix`"
const title = "title"
const header = "*header*"

func Test_progressLogStartStop(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	err := snConfig.SnapshotMulti("start", bufHandler.snap())
	require.NoError(t, err)
	pg.Stop(false)
	bufHandler.page()
	err = snConfig.SnapshotMulti("stop", bufHandler.snap())
	require.NoError(t, err)

}

func Test_progressLogLine(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	err := snConfig.SnapshotMulti("start", bufHandler.snap())
	require.NoError(t, err)
	writeThis := "Hello progress line"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	err = snConfig.SnapshotMulti("log", bufHandler.snap())
	require.NoError(t, err)
	pg.Stop(false)
	bufHandler.page()
	err = snConfig.SnapshotMulti("stop", bufHandler.snap())
	require.NoError(t, err)
}
func Test_progressLogMultiWrite(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	writeThis := "line: "
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	for index := range make([]int, 3) {
		w, err := pg.Write([]byte(fmt.Sprintf(", %x", index)))
		require.NoError(t, err)
		require.Equal(t, 3, w)
		bufHandler.page()
	}

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

func Test_progressLogWithBreak(t *testing.T) {
	sizeFn := func() int {
		return 40
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	writeThis := "line one\nline two\n\nlast line"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

func Test_progressLogStartWithBreak(t *testing.T) {
	sizeFn := func() int {
		return 40
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	writeThis := "\nhello,"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	writeThis = " azd"
	w, err = pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

func Test_progressLogLongLine(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}
	pg := newProgressLogWithWidthFn(5, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	// Should use 3 lines, b/c of the prefix
	writeThis := strings.Repeat("x", screenWidth*2)
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	bufHandler.page()

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

func Test_progressLogManyLines(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}
	linesToDisplay := 5
	pg := newProgressLogWithWidthFn(linesToDisplay, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	// Duplicating the lines to display to see log progress displaying only the last `linesToDisplay`
	for index := range make([]int, linesToDisplay*2) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
		bufHandler.page()
	}

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

// test structure that generates screen-time-travel snap
type testBufferHandler struct {
	bytes.Buffer
	pages       []string
	currentLine int
}

// produces the final snap from all pages
func (h *testBufferHandler) snap() string {

	return strings.Join(h.pages, "\n\n## Next state\n\n")
}

func offset(src, regex string) int {
	r := regexp.MustCompile(regex)
	match := r.FindSubmatch([]byte(src))
	if len(match) == 2 {
		var qty int
		qty, err := strconv.Atoi(string(match[1]))
		if err != nil {
			log.Panic("converting string to int: %w", err)
		}
		return qty
	}
	return 0
}

// uses the current buffer state to update the last page and produce a new page.
func (h *testBufferHandler) page() {
	var updatePage bool
	if len(h.pages) == 0 {
		screenLines := len(strings.Split(h.String(), "\n"))
		emptyScreen := strings.Join(make([]string, screenLines), "\n")
		h.pages = append(h.pages, emptyScreen)
		updatePage = true
	}
	lastPage := h.pages[len(h.pages)-1]
	lines := strings.Split(lastPage, "\n")

	bufferText := h.String()
	for len(bufferText) > 0 {
		if len(bufferText) < 4 {
			lines[h.currentLine] += bufferText[0:]
			bufferText = ""
			continue
		}

		pentaCode := bufferText[0:4]
		if pentaCode == tm.RESET_LINE {
			lines[h.currentLine] = ""
			bufferText = bufferText[4:]
			continue
		}

		// moving up
		if offsetUp := offset(pentaCode, `\x1b\[(\d+)A`); offsetUp > 0 {
			h.currentLine -= offsetUp
			bufferText = bufferText[4:]
			continue
		}

		// moving down
		if offsetDown := offset(pentaCode, `\x1b\[(\d+)B`); offsetDown > 0 {
			h.currentLine += offsetDown
			bufferText = bufferText[4:]
			continue
		}

		nextByte := bufferText[0]
		if nextByte == '\n' {
			h.currentLine++
			bufferText = bufferText[1:]
			continue
		}

		lines[h.currentLine] += bufferText[0:1]
		bufferText = bufferText[1:]
	}

	if updatePage {
		h.pages[len(h.pages)-1] = strings.Join(lines, "\n")
	} else {
		h.pages = append(h.pages, strings.Join(lines, "\n"))
	}
	h.Buffer = bytes.Buffer{}
}

func Test_progressChangeHeader(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}

	linesToDisplay := 5
	pg := newProgressLogWithWidthFn(linesToDisplay, prefix, title, header, sizeFn)

	snConfig := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(".md"))

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	// Duplicating the lines to display to see log progress displaying only the last `linesToDisplay`
	for index := range make([]int, linesToDisplay) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
		bufHandler.page()
	}

	pg.Header("*Updated Header Here*")
	bufHandler.page()

	for index := range make([]int, linesToDisplay) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
		bufHandler.page()
	}

	snConfig.SnapshotT(t, bufHandler.snap())
	pg.Stop(false)
}

func Test_progressLogDebugFile(t *testing.T) {
	// Set the environment variable for debug logging
	t.Setenv("AZD_DEBUG_PROGRESS_LOGS", "1")

	// Create a temporary directory for the test
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err)

	// Change to temp directory
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalWd)
		require.NoError(t, err)
	}()

	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}

	testTitle := "Test Debug Logs"
	pg := newProgressLogWithWidthFn(5, prefix, testTitle, header, sizeFn)

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer
	pg.Start()
	bufHandler.page()

	// Write some test data
	testData := []string{
		"First log line\n",
		"Second log line\n",
		"Third log line\n",
	}

	for _, data := range testData {
		_, err := pg.Write([]byte(data))
		require.NoError(t, err)
		bufHandler.page()
	}

	pg.Stop(false)

	// Check that a log file was created
	files, err := filepath.Glob("*.log")
	require.NoError(t, err)
	require.Len(t, files, 1, "Expected exactly one log file to be created")

	// Verify the filename contains sanitized title
	expectedPrefix := "Test_Debug_Logs-"
	require.True(
		t,
		strings.HasPrefix(filepath.Base(files[0]), expectedPrefix),
		"Expected filename to start with '%s', got '%s'",
		expectedPrefix,
		filepath.Base(files[0]),
	)

	// Read the file content and verify it contains the expected data
	content, err := os.ReadFile(files[0])
	require.NoError(t, err)

	contentStr := string(content)
	// Check that all test data is in the file
	for _, data := range testData {
		require.Contains(t, contentStr, strings.TrimSpace(data), "Expected log file to contain: %s", data)
	}

	// Verify the file contains the header and title
	require.Contains(t, contentStr, header, "Expected log file to contain header")
}

func Test_progressLogReuseWithDebugFile(t *testing.T) {
	// Set the environment variable for debug logging
	t.Setenv("AZD_DEBUG_PROGRESS_LOGS", "1")

	// Create a temporary directory for the test
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err)

	// Change to temp directory
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalWd)
		require.NoError(t, err)
	}()

	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}

	testTitle := "Reuse Test"
	pg := newProgressLogWithWidthFn(5, prefix, testTitle, header, sizeFn)

	var bufHandler testBufferHandler
	tm.Screen = &bufHandler.Buffer

	// First use
	pg.Start()
	bufHandler.page()
	_, err = pg.Write([]byte("First use line 1\n"))
	require.NoError(t, err)
	bufHandler.page()
	_, err = pg.Write([]byte("First use line 2\n"))
	require.NoError(t, err)
	bufHandler.page()
	pg.Stop(false)

	// Wait a bit to ensure different timestamp
	time.Sleep(1100 * time.Millisecond)

	// Second use - this should not panic
	pg.Start()
	bufHandler.page()
	_, err = pg.Write([]byte("Second use line 1\n"))
	require.NoError(t, err)
	bufHandler.page()
	_, err = pg.Write([]byte("Second use line 2\n"))
	require.NoError(t, err)
	bufHandler.page()
	pg.Stop(false)

	// Wait a bit to ensure different timestamp
	time.Sleep(1100 * time.Millisecond)

	// Third use - just to be sure
	pg.Start()
	bufHandler.page()
	_, err = pg.Write([]byte("Third use line 1\n"))
	require.NoError(t, err)
	bufHandler.page()
	pg.Stop(false)

	// Check that log files were created (should be at least 1, might be up to 3 depending on timing)
	files, err := filepath.Glob("*.log")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 1, "Expected at least one log file to be created")
	require.LessOrEqual(t, len(files), 3, "Expected at most three log files to be created")

	// Verify that at least one file contains content from each use
	allContent := ""
	for _, file := range files {
		content, err := os.ReadFile(file)
		require.NoError(t, err)
		allContent += string(content)
	}

	// Each use should have written something
	require.Contains(t, allContent, "First use line 1", "Expected log to contain first use content")
	require.Contains(t, allContent, "Second use line 1", "Expected log to contain second use content")
	require.Contains(t, allContent, "Third use line 1", "Expected log to contain third use content")
}
