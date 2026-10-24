// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// defaultWaitDelay bounds shutdown when descendants retain inherited I/O.
const defaultWaitDelay = 2 * time.Second

var errFailedStartWaitTimeout = errors.New(
	"timed out waiting to reap process after managed process setup failed",
)

// Options controls how a process scope is managed.
type Options struct {
	Interactive bool
	// ForwardInterrupts keeps interactive Windows commands in the parent's
	// console process group so they receive Ctrl+C directly.
	ForwardInterrupts        bool
	TerminateRemainingOnWait bool
}

type cancelOutcome uint8

const (
	cancelNotAttempted cancelOutcome = iota
	cancelInterrupted
	cancelAlreadyFinished
	cancelFailed
)

// Tree manages an exec.Cmd and its platform-specific process scope.
type Tree struct {
	Cmd     *exec.Cmd
	ctx     context.Context
	options Options

	platform platformState

	cancelOnce    sync.Once
	cancelResult  error
	cancelMu      sync.Mutex
	cancelOutcome cancelOutcome
	cancelErr     error
	finished      atomic.Bool
}

// New creates a process manager for cmd.
func New(ctx context.Context, cmd *exec.Cmd, options Options) *Tree {
	tree := &Tree{
		Cmd:     cmd,
		ctx:     ctx,
		options: options,
	}

	if cmd.Cancel != nil {
		cmd.Cancel = tree.cancel
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = defaultWaitDelay
	}

	return tree
}

// Start starts the command and completes platform process setup.
func (t *Tree) Start() error {
	configure(t)

	if err := t.Cmd.Start(); err != nil {
		if t.ctx != nil && errors.Is(err, t.ctx.Err()) {
			if cause := context.Cause(t.ctx); cause != nil {
				err = cause
			}
		}
		startFailed(t, err)
		return err
	}

	if err := finishStart(t); err != nil {
		cleanupErr := t.cleanupFailedStart()
		if cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}

	return nil
}

// Wait waits for the command, its cancellation watcher, and redirected I/O to finish.
func (t *Tree) Wait() error {
	if t.Cmd.Process == nil {
		return t.Cmd.Wait()
	}

	err := t.waitForCommand()
	cause := t.cancellationCause()
	// os/exec reports ctx.Err when its watcher cancels a command, even when
	// the context has a more specific custom cause. Exact identity preserves
	// wrapped process and I/O errors.
	if cause != nil && t.ctx != nil && err == t.ctx.Err() { //nolint:errorlint
		err = cause
	}
	waitDelayExpired := errors.Is(err, exec.ErrWaitDelay)
	exitErr, _ := errors.AsType[*exec.ExitError](err)
	interrupted := exitErr != nil && IsInterruptExit(exitErr, exitErr.ExitCode())
	// Successful scripting commands may intentionally leave background children,
	// but an interrupted root must not release them before host cancellation arrives.
	terminateRemainingOnWait := t.options.TerminateRemainingOnWait ||
		waitDelayExpired ||
		interrupted

	if terminateRemainingOnWait {
		if terminateErr := terminateRemaining(t); terminateErr != nil &&
			!errors.Is(terminateErr, os.ErrProcessDone) {
			err = errors.Join(
				err,
				fmt.Errorf("terminating remaining processes: %w", terminateErr),
			)
		}
	}

	if releaseErr := release(t, cause != nil || terminateRemainingOnWait); releaseErr != nil {
		releaseErr = fmt.Errorf("releasing managed process resources: %w", releaseErr)
		if err == nil {
			err = releaseErr
		} else {
			err = errors.Join(err, releaseErr)
		}
	}

	if cause == nil || errors.Is(err, cause) {
		return err
	}
	if err == nil {
		return cause
	}

	return &causedError{
		err:   err,
		cause: cause,
	}
}

func (t *Tree) waitForCommand() error {
	waitResult := make(chan error, 1)
	go func() {
		err := t.Cmd.Wait()
		t.finished.Store(true)
		waitResult <- err
	}()

	var err error
	select {
	case err = <-waitResult:
	default:
		select {
		case err = <-waitResult:
		case <-t.contextDone():
			// Prefer a command result that completed concurrently with cancellation.
			select {
			case err = <-waitResult:
			default:
				// The root may already have exited while Cmd.Wait remains blocked on
				// inherited I/O. Terminate descendants, but keep the root's eventual
				// wait result primary and attach the cancellation cause in Wait.
				_ = t.cancel()
				err = <-waitResult
			}
		}
	}
	return err
}

// Kill forcibly terminates the managed process scope.
func (t *Tree) Kill() error {
	if t.Cmd.Process == nil || t.finished.Load() {
		return os.ErrProcessDone
	}
	return terminate(t)
}

func (t *Tree) cancellationCause() error {
	t.cancelMu.Lock()
	defer t.cancelMu.Unlock()

	switch t.cancelOutcome {
	case cancelInterrupted:
		return t.contextCause()
	case cancelFailed:
		if cause := t.contextCause(); cause != nil {
			return errors.Join(
				cause,
				fmt.Errorf("terminating managed process scope: %w", t.cancelErr),
			)
		}
	case cancelNotAttempted, cancelAlreadyFinished:
	}

	return nil
}

func (t *Tree) contextCause() error {
	if t.ctx == nil {
		return nil
	}
	return context.Cause(t.ctx)
}

func (t *Tree) cancel() error {
	t.cancelOnce.Do(func() {
		t.cancelResult = t.cancelProcess()
	})
	return t.cancelResult
}

func (t *Tree) cancelProcess() error {
	if t.Cmd.Process == nil || t.finished.Load() {
		return os.ErrProcessDone
	}
	err := terminate(t)

	t.cancelMu.Lock()
	defer t.cancelMu.Unlock()

	switch {
	case err == nil:
		t.cancelOutcome = cancelInterrupted
	case errors.Is(err, os.ErrProcessDone):
		t.cancelOutcome = cancelAlreadyFinished
	default:
		t.cancelOutcome = cancelFailed
		t.cancelErr = err
	}

	return err
}

func (t *Tree) contextDone() <-chan struct{} {
	if t.ctx == nil {
		return nil
	}
	return t.ctx.Done()
}

func (t *Tree) cleanupFailedStart() error {
	return cleanupStartedProcess(
		t.Cmd.Process.Kill,
		func() error {
			err := t.Cmd.Wait()
			t.finished.Store(true)
			return err
		},
		func() error {
			return release(t, true)
		},
		t.Cmd.WaitDelay,
	)
}

func cleanupStartedProcess(
	kill func() error,
	wait func() error,
	release func() error,
	waitDelay time.Duration,
) error {
	var errs []error
	if err := kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		errs = append(
			errs,
			fmt.Errorf("terminating process after managed process setup failed: %w", err),
		)
	}
	if err := release(); err != nil {
		errs = append(
			errs,
			fmt.Errorf("releasing managed process resources: %w", err),
		)
	}

	if waitDelay <= 0 {
		waitDelay = defaultWaitDelay
	}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- wait()
	}()

	timer := time.NewTimer(waitDelay)
	defer timer.Stop()

	select {
	case err := <-waitResult:
		if err != nil {
			errs = append(
				errs,
				fmt.Errorf("waiting for process after managed process setup failed: %w", err),
			)
		}
	case <-timer.C:
		errs = append(
			errs,
			fmt.Errorf("%w after %s", errFailedStartWaitTimeout, waitDelay),
		)
	}

	return errors.Join(errs...)
}

type causedError struct {
	err   error
	cause error
}

func (e *causedError) Error() string {
	return e.err.Error()
}

func (e *causedError) Unwrap() []error {
	return []error{e.err, e.cause}
}
