package async

// Progress is a wrapper around a channel which can be used to report progress of an operation.
type Progress[T comparable] struct {
	progressChannel chan T
}

func NewProgress[T comparable]() *Progress[T] {
	return &Progress[T]{
		progressChannel: make(chan T),
	}
}

func (p *Progress[T]) Progress() <-chan T {
	return p.progressChannel
}

// Done closes the underlying channel, signaling no more progress will be reported. It is an error to call SetProgress after
// calling Done.
func (p *Progress[T]) Done() {
	close(p.progressChannel)
}

func (p *Progress[T]) SetProgress(progress T) {
	p.progressChannel <- progress
}
