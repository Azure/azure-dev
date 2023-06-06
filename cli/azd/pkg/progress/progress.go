package progress

import "github.com/azure/azure-dev/cli/azd/pkg/messaging"

var ProgressMessageKind messaging.MessageKind = "progress"

type StateKind string

const (
	Running StateKind = "running"
	Success StateKind = "success"
	Error   StateKind = "error"
)

// ProgressMessage is a sample message payload that contains the message to display and the state of the message
// This ends up being the payload of the message sent to the topic
type ProgressMessage struct {
	Message string
	State   StateKind
}

// NewProgressMessage is a helper function to create a new message payload
// Any message payload can be sent to the topic as long as it is wrapping in the `messaging.Message` struct
func NewProgressMessage(message string, state StateKind) *messaging.Message {
	msg := &ProgressMessage{
		Message: message,
		State:   state,
	}

	return messaging.NewMessage(ProgressMessageKind, msg)
}
