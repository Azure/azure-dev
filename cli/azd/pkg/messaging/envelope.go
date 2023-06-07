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

// Envelope represents a message sent to a topic.
type Envelope struct {
	Timestamp time.Time
	Type      MessageKind
	Value     any
	Tags      map[string]any
}

// NewEnvelope creates a new message with the specified kind and value.
func NewEnvelope[T any](kind MessageKind, value T) *Envelope {
	return &Envelope{
		Type:      kind,
		Value:     value,
		Timestamp: time.Now(),
		Tags:      map[string]any{},
	}
}
