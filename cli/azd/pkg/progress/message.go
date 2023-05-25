package progress

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
)

const MessageKind messaging.MessageKind = "Progress"

type Message struct {
	Message string
}

func NewMessage(message string) *messaging.Message {
	progressMessage := &Message{
		Message: message,
	}

	return messaging.NewMessage(MessageKind, progressMessage)
}

func (m *Message) Print(ctx context.Context, printer *Printer) {
	printer.Progress(ctx, m.Message)
}
