package operations

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/google/uuid"
)

type messagePrinter struct {
	console            input.Console
	subscriber         messaging.Subscriber
	subscription       *messaging.Subscription
	currentMessage     string
	currentOperationId uuid.UUID
	spinnerRunning     bool
}

func NewMessagePrinter(subscriber messaging.Subscriber, console input.Console) Printer {
	return &messagePrinter{
		subscriber: subscriber,
		console:    console,
	}
}

// Starts listening for messages to print to the console
func (p *messagePrinter) Start(ctx context.Context) error {
	if p.subscription != nil {
		return fmt.Errorf("printer already started")
	}

	filter := func(ctx context.Context, message *messaging.Envelope) bool {
		return message.Type == defaultMessageKind
	}

	subscription, err := p.subscriber.Subscribe(ctx, filter, p.receiveMessage)
	if err != nil {
		return err
	}

	p.subscription = subscription
	return nil
}

// Stops listening for messages
func (p *messagePrinter) Stop(ctx context.Context) error {
	if p.subscription == nil {
		return fmt.Errorf("printer not started")
	}

	subscrption := p.subscription
	p.subscription = nil
	return subscrption.Close(ctx)
}

// Flushes any pending messages and blocks until they have all been handled
func (p *messagePrinter) Flush(ctx context.Context) error {
	if p.subscription == nil {
		return fmt.Errorf("printer not started")
	}

	return p.subscription.Flush(ctx)
}

// Receives messages from the message bus and prints them to the console
func (p *messagePrinter) receiveMessage(ctx context.Context, envelope *messaging.Envelope) {
	msg, ok := envelope.Value.(*Message)
	if !ok {
		return
	}

	switch msg.State {
	case StateRunning:
		var displayMessage string
		// New operation, start spinner
		if p.currentOperationId == uuid.Nil {
			displayMessage = msg.Message
			p.currentMessage = msg.Message
			p.currentOperationId = msg.CorrelationId
		} else { // Existing operation in progress, report as progress
			displayMessage = fmt.Sprintf("%s (%s)", p.currentMessage, msg.Message)
		}

		p.console.ShowSpinner(ctx, displayMessage, input.Step)
	case StateProgress:
		if p.currentMessage != "" {
			displayMessage := fmt.Sprintf("%s (%s)", p.currentMessage, msg.Message)
			p.console.ShowSpinner(ctx, displayMessage, input.Step)
		}
	case StateSuccess, StateError, StateWarning, StateSkipped:
		var spinnerType input.SpinnerUxType
		switch msg.State {
		case StateSuccess:
			spinnerType = input.StepDone
		case StateError:
			spinnerType = input.StepFailed
		case StateWarning:
			spinnerType = input.StepWarning
		case StateSkipped:
			spinnerType = input.StepSkipped
		}

		// We only stop the spinner and reset the state if messages are from the same operation
		if p.currentOperationId == msg.CorrelationId {
			p.console.StopSpinner(ctx, p.currentMessage, spinnerType)
			p.reset()
		}
	default:
		log.Printf("unknown operation state %s", msg.State)
	}
}

func (p *messagePrinter) reset() {
	p.currentMessage = ""
	p.currentOperationId = uuid.Nil
	p.spinnerRunning = false
}
