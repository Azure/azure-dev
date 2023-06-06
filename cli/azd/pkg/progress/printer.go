package progress

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
)

type Printer struct {
	// Import subscriber to register handler for message stream
	subscriber messaging.Subscriber
	console    input.Console
}

func NewPrinter(subscriber messaging.Subscriber, console input.Console) *Printer {
	return &Printer{
		subscriber: subscriber,
		console:    console,
	}
}

func (p *Printer) Start(ctx context.Context) (*messaging.Subscription, error) {
	// Create a filter to only receive progress messages
	filter := func(ctx context.Context, message *messaging.Message) bool {
		return message.Type == ProgressMessageKind
	}

	// Subscribe to the topic and handle messages
	subscription, err := p.subscriber.Subscribe(ctx, filter, func(ctx context.Context, msg *messaging.Message) {
		// Message payload can contain any value
		// Cast to appropriate type
		progressMessage, ok := msg.Value.(*ProgressMessage)
		if !ok {
			return
		}

		// Integrate with existing UX components for writing out to stdout / stderr
		switch progressMessage.State {
		case Running:
			p.console.ShowSpinner(ctx, progressMessage.Message, input.Step)
		case Success:
			p.console.StopSpinner(ctx, progressMessage.Message, input.StepDone)
		}
	})

	if err != nil {
		return nil, err
	}

	return subscription, nil
}
