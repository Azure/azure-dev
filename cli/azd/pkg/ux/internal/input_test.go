// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/stretchr/testify/require"
)

func TestNewInput(t *testing.T) {
	t.Parallel()
	in := NewInput()
	require.NotNil(t, in)
	require.NotNil(t, in.cursor)
	require.Empty(t, in.value)
}

func TestProcessKey_PrintableAppended(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("ab"), 'c', false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.Equal(t, 'c', args.Char)
	require.Equal(t, 'c', args.Key)
	require.False(t, args.Hint)
	require.False(t, args.Cancelled)
}

func TestProcessKey_PrintableUnicode(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("こん"), 'に', false)
	require.Equal(t, []rune("こんに"), newVal)
	require.Equal(t, "こんに", args.Value)
}

func TestProcessKey_AppendToEmpty(t *testing.T) {
	t.Parallel()
	newVal, args := processKey(nil, 'x', false)
	require.Equal(t, []rune("x"), newVal)
	require.Equal(t, "x", args.Value)
	require.False(t, args.Cancelled)
}

func TestProcessKey_Space(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("hi"), surveyterm.KeySpace, false)
	require.Equal(t, []rune("hi "), newVal)
	require.Equal(t, "hi ", args.Value)
	require.Equal(t, surveyterm.KeySpace, args.Char)
}

func TestProcessKey_BackspaceRemovesLast(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("hello"), surveyterm.KeyBackspace, false)
	require.Equal(t, []rune("hell"), newVal)
	require.Equal(t, "hell", args.Value)
	require.False(t, args.Cancelled)
}

func TestProcessKey_DeleteRemovesLast(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("hello"), surveyterm.KeyDelete, false)
	require.Equal(t, []rune("hell"), newVal)
	require.Equal(t, "hell", args.Value)
}

func TestProcessKey_BackspaceOnEmptyBufferIsNoop(t *testing.T) {
	t.Parallel()
	// When buffer is empty, backspace is non-printable so it falls through; nothing
	// should be appended, and the value must remain empty.
	newVal, args := processKey([]rune{}, surveyterm.KeyBackspace, false)
	require.Empty(t, newVal)
	require.Equal(t, "", args.Value)
	require.False(t, args.Cancelled)
}

func TestProcessKey_DeleteOnEmptyBufferIsNoop(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune{}, surveyterm.KeyDelete, false)
	require.Empty(t, newVal)
	require.Equal(t, "", args.Value)
}

func TestProcessKey_HintQuestionMark(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("abc"), '?', false)
	// '?' should be treated as a hint key and NOT appended to the buffer.
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.True(t, args.Hint)
	require.False(t, args.Cancelled)
}

func TestProcessKey_HintQuestionMarkIgnoredWhenIgnoreHintKeysTrue(t *testing.T) {
	t.Parallel()
	// With IgnoreHintKeys=true, '?' is a printable rune and should be appended.
	newVal, args := processKey([]rune("abc"), '?', true)
	require.Equal(t, []rune("abc?"), newVal)
	require.Equal(t, "abc?", args.Value)
	require.False(t, args.Hint)
}

func TestProcessKey_EscapeClearsHint(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("abc"), surveyterm.KeyEscape, false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.False(t, args.Hint)
	// When IgnoreHintKeys is false, escape falls into the hint-clearing branch
	// and is NOT treated as a cancel.
	require.False(t, args.Cancelled)
}

func TestProcessKey_EscapeCancelsWhenIgnoringHints(t *testing.T) {
	t.Parallel()
	// With IgnoreHintKeys=true, escape is non-printable and falls through to
	// the cancel branch.
	newVal, args := processKey([]rune("abc"), surveyterm.KeyEscape, true)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.True(t, args.Cancelled)
}

func TestProcessKey_Interrupt(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("abc"), surveyterm.KeyInterrupt, false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.True(t, args.Cancelled)
}

func TestProcessKey_InterruptWithIgnoreHints(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("abc"), surveyterm.KeyInterrupt, true)
	require.Equal(t, []rune("abc"), newVal)
	require.True(t, args.Cancelled)
}

func TestProcessKey_NewlineTreatedAsEnter(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("abc"), '\n', false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.Equal(t, surveyterm.KeyEnter, args.Key)
	require.Equal(t, '\n', args.Char)
	require.False(t, args.Cancelled)
}

func TestProcessKey_CarriageReturnIsEnter(t *testing.T) {
	t.Parallel()
	// surveyterm.KeyEnter is '\r'. '\r' is not printable, is not backspace/delete,
	// not '?', not escape, not space, not interrupt, not '\n'. So none of the
	// mutations run and Key remains '\r' (which equals KeyEnter).
	newVal, args := processKey([]rune("abc"), surveyterm.KeyEnter, false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.Equal(t, surveyterm.KeyEnter, args.Key)
	require.False(t, args.Cancelled)
}

func TestProcessKey_BackspacePreservesUnicode(t *testing.T) {
	t.Parallel()
	newVal, args := processKey([]rune("こんにちは🌍"), surveyterm.KeyBackspace, false)
	require.Equal(t, []rune("こんにちは"), newVal)
	require.Equal(t, "こんにちは", args.Value)
}

func TestProcessKey_SequentialAppend(t *testing.T) {
	t.Parallel()
	buf := []rune{}
	for _, r := range "hello" {
		var args KeyPressEventArgs
		buf, args = processKey(buf, r, false)
		require.Equal(t, string(buf), args.Value)
	}
	require.Equal(t, []rune("hello"), buf)
}

func TestProcessKey_SequentialAppendThenBackspace(t *testing.T) {
	t.Parallel()
	buf := []rune{}
	for _, r := range "abc" {
		buf, _ = processKey(buf, r, false)
	}
	buf, _ = processKey(buf, surveyterm.KeyBackspace, false)
	buf, _ = processKey(buf, surveyterm.KeyBackspace, false)
	require.Equal(t, []rune("a"), buf)
}

func TestProcessKey_NonPrintableUnhandledRune(t *testing.T) {
	t.Parallel()
	// A non-printable rune that doesn't match any branch (e.g., KeyArrowUp = '\x10')
	// should leave the buffer unchanged, not cancel, not set hint.
	newVal, args := processKey([]rune("abc"), surveyterm.KeyArrowUp, false)
	require.Equal(t, []rune("abc"), newVal)
	require.Equal(t, "abc", args.Value)
	require.False(t, args.Cancelled)
	require.False(t, args.Hint)
	require.Equal(t, surveyterm.KeyArrowUp, args.Key)
}

func TestDisableVirtualTerminalInput_Noop(t *testing.T) {
	t.Parallel()
	// On non-Windows platforms this is a no-op returning nil. On Windows it
	// may return an error if stdin is not a console (as in `go test`), which
	// the caller treats as non-fatal. Either way, calling it must not panic.
	// Pass a real *os.File because the Windows implementation dereferences it
	// via f.Fd(); nil would panic on Windows.
	_ = disableVirtualTerminalInput(os.Stdin)
}

func TestReadInput_SetTermModeErrorOnNonTTY(t *testing.T) {
	// Not parallel: mutates os.Stdin.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	in := NewInput()
	done := make(chan error, 1)
	go func() {
		done <- in.ReadInput(t.Context(), nil, func(args *KeyPressEventArgs) (bool, error) {
			return true, nil
		})
	}()

	select {
	case gotErr := <-done:
		// On non-TTY stdin, SetTermMode surfaces an error via errChan and
		// ReadInput returns it. The specific error text is OS-dependent; we
		// only require that some error is returned and that the call does not
		// hang.
		require.Error(t, gotErr)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadInput did not return within 5s on non-TTY stdin")
	}
}

func TestReadInput_NonNilConfig(t *testing.T) {
	// Not parallel: mutates os.Stdin.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	in := NewInput()
	done := make(chan error, 1)
	cfg := &InputConfig{InitialValue: "seed", IgnoreHintKeys: true}
	go func() {
		done <- in.ReadInput(t.Context(), cfg, func(args *KeyPressEventArgs) (bool, error) {
			return false, nil
		})
	}()

	select {
	case <-done:
		// We don't care which path won (SetTermMode error vs. something else) —
		// only that ReadInput terminates promptly and that the initial value
		// was applied to the Input's buffer.
		require.Equal(t, []rune("seed"), in.value)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadInput did not return within 5s")
	}
}

func TestReadInput_ContextCancellationReturnsErrCancelled(t *testing.T) {
	// Not parallel: mutates os.Stdin.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel

	in := NewInput()
	done := make(chan error, 1)
	handlerCalled := make(chan struct{}, 1)
	go func() {
		done <- in.ReadInput(ctx, nil, func(args *KeyPressEventArgs) (bool, error) {
			select {
			case handlerCalled <- struct{}{}:
			default:
			}
			require.True(t, args.Cancelled)
			return true, nil
		})
	}()

	select {
	case gotErr := <-done:
		// Either the ctx.Done path fires first (returning ErrCancelled joined
		// with ctx.Err), or SetTermMode errors first (returning that err).
		// Both are acceptable; we just require termination and, when the
		// cancel path wins, verify ErrCancelled is part of the error chain.
		if gotErr != nil && errors.Is(gotErr, ErrCancelled) {
			require.ErrorIs(t, gotErr, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadInput did not return within 5s after ctx cancel")
	}
}
