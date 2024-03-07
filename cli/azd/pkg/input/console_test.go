// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lineCapturer struct {
	captured []string
}

func (l *lineCapturer) Write(bytes []byte) (n int, err error) {
	var sb strings.Builder
	for i, b := range bytes {
		if b == '\n' {
			l.captured = append(l.captured, sb.String())
			sb.Reset()
			continue
		}

		err = sb.WriteByte(b)
		if err != nil {
			return i, err
		}

	}
	return len(bytes), nil
}

// Verifies no extra output is printed in non-tty scenarios for the spinner.
func TestAskerConsole_Spinner_NonTty(t *testing.T) {
	// The underlying spinner relies on non-blocking channels for paint updates.
	// We need to give it some time to paint.
	const cSleep = 50 * time.Millisecond

	lines := &lineCapturer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: lines},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: lines,
		},
		nil)

	ctx := context.Background()
	require.Len(t, lines.captured, 0)
	c.ShowSpinner(ctx, "Some title.", Step)
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 1)
	require.Equal(t, lines.captured[0], "Some title.")

	c.ShowSpinner(ctx, "Some title 2.", Step)
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 2)
	require.Equal(t, lines.captured[1], "Some title 2.")

	c.StopSpinner(ctx, "", StepDone)
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 2)
	require.Equal(t, lines.captured[1], "Some title 2.")

	c.ShowSpinner(ctx, "Some title 3.", Step)
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 3)
	require.Equal(t, lines.captured[2], "Some title 3.")

	c.Message(ctx, "Some message.")
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 4)
	require.Equal(t, lines.captured[3], "Some message.")

	c.StopSpinner(ctx, "Done.", StepDone)
	time.Sleep(cSleep)
	require.Len(t, lines.captured, 5)
}
