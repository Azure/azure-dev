// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	tm "github.com/buger/goterm"
	"github.com/stretchr/testify/require"
)

const prefix = "<prefix>"

func Test_progressLogStartStop(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()
	snConfig.SnapshotMulti("start", decodeScreenString(stdout.String()))
	pg.Stop()
	snConfig.SnapshotMulti("stop", decodeScreenString(stdout.String()))
}

func decodeScreenString(encoded string) string {
	decodedResult := strings.ReplaceAll(encoded, tm.RESET_LINE, "<RL>")
	decodedResult = strings.ReplaceAll(decodedResult, "\033[1A", "<M1U>")
	decodedResult = strings.ReplaceAll(decodedResult, "\033[2A", "<M2U>")
	decodedResult = strings.ReplaceAll(decodedResult, "\033[4A", "<M4U>")
	decodedResult = strings.ReplaceAll(decodedResult, "\033[3B", "<M3D>")
	return decodedResult
}

func Test_progressLogLine(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()
	snConfig.SnapshotMulti("start", decodeScreenString(stdout.String()))
	writeThis := "Hello progress line"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)
	snConfig.SnapshotMulti("log", decodeScreenString(stdout.String()))
	pg.Stop()
	snConfig.SnapshotMulti("stop", decodeScreenString(stdout.String()))
}
func Test_progressLogMultiWrite(t *testing.T) {

	sizeFn := func() int {
		return 40
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	writeThis := "line: "
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)

	for index := range make([]int, 3) {
		w, err := pg.Write([]byte(fmt.Sprintf(", %x", index)))
		require.NoError(t, err)
		require.Equal(t, 3, w)
	}

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}

func Test_progressLogWithBreak(t *testing.T) {
	sizeFn := func() int {
		return 40
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	writeThis := "line one\nline two\n\nlast line"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}

func Test_progressLogStartWithBreak(t *testing.T) {
	sizeFn := func() int {
		return 40
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	writeThis := "\nhello,"
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)

	writeThis = " azd"
	w, err = pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}

func Test_progressLogLongLine(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}
	var stdout bytes.Buffer
	pg := NewProgressLogWithSizeFn(5, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	// Should use 3 lines, b/c of the prefix
	writeThis := strings.Repeat("x", screenWidth*2)
	w, err := pg.Write([]byte(writeThis))
	require.NoError(t, err)
	require.Equal(t, len(writeThis), w)

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}

func Test_progressLogManyLines(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}
	var stdout bytes.Buffer
	linesToDisplay := 5
	pg := NewProgressLogWithSizeFn(linesToDisplay, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	// Duplicating the lines to display to see log progress displaying only the last `linesToDisplay`
	for index := range make([]int, linesToDisplay*2) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
	}

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}

func Test_progressChangeHeader(t *testing.T) {
	screenWidth := 40
	sizeFn := func() int {
		return screenWidth
	}
	var stdout bytes.Buffer
	linesToDisplay := 5
	pg := NewProgressLogWithSizeFn(linesToDisplay, prefix, "title", "header", sizeFn)

	snConfig := snapshot.NewDefaultConfig()

	tm.Screen = &stdout
	pg.Start()

	// Duplicating the lines to display to see log progress displaying only the last `linesToDisplay`
	for index := range make([]int, linesToDisplay) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
	}

	pg.Header("Updated Header Here")

	for index := range make([]int, linesToDisplay) {
		_, err := pg.Write([]byte(fmt.Sprintf("line: %x\n", index)))
		require.NoError(t, err)
	}

	snConfig.SnapshotT(t, decodeScreenString(stdout.String()))
	pg.Stop()
}
