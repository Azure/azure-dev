package operations

import (
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/google/uuid"
)

type StateKind string

const defaultMessageKind messaging.MessageKind = "operation"

var (
	StateRunning  StateKind = "running"
	StateSuccess  StateKind = "success"
	StateError    StateKind = "error"
	StateWarning  StateKind = "warning"
	StateProgress StateKind = "progress"
	StateSkipped  StateKind = "skipped"
)

type Message struct {
	Message       string
	CorrelationId uuid.UUID
	State         StateKind
}

func NewMessage(message string, state StateKind) (*messaging.Envelope, *Message) {
	msg := &Message{
		CorrelationId: uuid.New(),
		Message:       message,
		State:         state,
	}

	return messaging.NewEnvelope(defaultMessageKind, msg), msg
}

func NewCorrelatedMessage(id uuid.UUID, message string, state StateKind) (*messaging.Envelope, *Message) {
	msg := &Message{
		CorrelationId: id,
		Message:       message,
		State:         state,
	}

	return messaging.NewEnvelope(defaultMessageKind, msg), msg
}
