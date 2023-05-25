package progress

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
)

type PrintableMessage interface {
	Print(ctx context.Context, printer *Printer)
}

type Printer struct {
	subscriber     messaging.Subscriber
	console        input.Console
	currentMessage string
}

func NewPrinter(subscriber messaging.Subscriber, console input.Console) *Printer {
	return &Printer{
		subscriber: subscriber,
		console:    console,
	}
}

func (p *Printer) CurrentMessage() string {
	return p.currentMessage
}

func (p *Printer) Register(ctx context.Context, progressFilter messaging.MessageFilter) *messaging.Subscription {
	return p.subscriber.Subscribe(ctx, progressFilter, p.writeMessage)
}

func (p *Printer) Message(ctx context.Context, message string) {
	p.console.Message(ctx, message)
}

func (p *Printer) Start(ctx context.Context, defaultMessage string) {
	p.currentMessage = defaultMessage
	p.console.ShowSpinner(ctx, defaultMessage, input.Step)
}

func (p *Printer) Progress(ctx context.Context, status string) {
	progressMessage := fmt.Sprintf("%s (%s)", p.currentMessage, status)
	p.console.ShowSpinner(ctx, progressMessage, input.Step)
}

func (p *Printer) Done(ctx context.Context) {
	p.console.StopSpinner(ctx, p.currentMessage, input.StepDone)
	p.currentMessage = ""
}

func (p *Printer) Fail(ctx context.Context) {
	p.console.StopSpinner(ctx, p.currentMessage, input.StepFailed)
	p.currentMessage = ""
}

func (p *Printer) Warn(ctx context.Context) {
	p.console.StopSpinner(ctx, p.currentMessage, input.StepWarning)
	p.currentMessage = ""
}

func (p *Printer) Skip(ctx context.Context) {
	p.console.StopSpinner(ctx, p.currentMessage, input.StepSkipped)
	p.currentMessage = ""
}

func (p *Printer) writeMessage(ctx context.Context, msg *messaging.Message) {
	printableMessage, ok := msg.Value.(PrintableMessage)
	if !ok {
		log.Printf("Message of type '%s' is not printable", msg.Type)
		return
	}

	printableMessage.Print(ctx, p)
}
