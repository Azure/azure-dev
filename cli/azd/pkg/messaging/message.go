package messaging

import (
	"time"
)

type MessageKind string

const (
	SimpleMessage MessageKind = "Simple"
)

type Message struct {
	Timestamp time.Time
	Type      MessageKind
	Value     any
	Tags      map[string]any
}

func NewMessage[T any](kind MessageKind, value T) *Message {
	return &Message{
		Type:      kind,
		Value:     value,
		Timestamp: time.Now(),
		Tags:      map[string]any{},
	}
}
