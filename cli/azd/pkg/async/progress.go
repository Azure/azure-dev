// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package async

// Progress is a wrapper around a channel which can be used to report progress of an operation. The zero value of Progress
// is invalid. Use [NewProgress] to create a new instance.
type Progress[T comparable] struct {
	progressChannel chan T
}

// NewProgress creates a new instance of Progress.
func NewProgress[T comparable]() *Progress[T] {
	return &Progress[T]{
		progressChannel: make(chan T),
	}
}

// NewNoopProgress creates a new instance of Progress that does not report any progress. The progress channel is drained
func NewNoopProgress[T comparable]() *Progress[T] {
	p := NewProgress[T]()
	go func() {
		for range p.Progress() {
			// Nothing to do here but we need to drain the channel to avoid blocking
		}
	}()

	return p
}

// Progress returns the read side of the underlying channel. The channel will be closed when [Done] is called, so a `range`
// loop may be used to consume all progress updates.
func (p *Progress[T]) Progress() <-chan T {
	return p.progressChannel
}

// Done closes the underlying channel, signaling no more progress will be reported. It is an error to call SetProgress after
// calling Done.
func (p *Progress[T]) Done() {
	close(p.progressChannel)
}

// SetProgress reports progress to the channel.
func (p *Progress[T]) SetProgress(progress T) {
	p.progressChannel <- progress
}

// RunWithProgress runs a function with a background goroutine reporting and progress to an observer.
func RunWithProgress[T comparable, R any](
	observer func(T),
	f func(*Progress[T]) (R, error),
) (R, error) {
	progress := NewProgress[T]()
	done := make(chan struct{})
	go func() {
		for p := range progress.Progress() {
			observer(p)
		}
		close(done)
	}()
	res, err := f(progress)
	progress.Done()
	<-done
	return res, err
}

// RunWithProgressE runs a function with a background goroutine reporting and progress to an observer.
func RunWithProgressE[T comparable](
	observer func(T),
	f func(*Progress[T]) error,
) error {
	progress := NewProgress[T]()
	done := make(chan struct{})
	go func() {
		for p := range progress.Progress() {
			observer(p)
		}
		close(done)
	}()
	err := f(progress)
	progress.Done()
	<-done
	return err
}
