package messaging

import (
	"time"
)

// MessageKind represents the kind of a message.
// This is used to filter messages.
type MessageKind string

const (
	// SimpleMessage is a simple message.
	SimpleMessage MessageKind = "Simple"
)

// Message represents a message sent to a topic.
type Message struct {
	Timestamp time.Time
	Type      MessageKind
	Value     any
	Tags      map[string]any
}

// NewMessage creates a new message with the specified kind and value.
func NewMessage[T any](kind MessageKind, value T) *Message {
	return &Message{
		Type:      kind,
		Value:     value,
		Timestamp: time.Now(),
		Tags:      map[string]any{},
	}
}
