package progress

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type Printer struct {
	subscriber     messaging.Subscriber
	console        input.Console
	defaultMessage string
}

func NewPrinter(subscriber messaging.Subscriber, console input.Console) *Printer {
	return &Printer{
		subscriber: subscriber,
		console:    console,
	}
}

func (p *Printer) Register(ctx context.Context, progressFilter messaging.MessageFilter) *messaging.Subscription {
	return p.subscriber.Subscribe(ctx, progressFilter, p.writeMessage)
}

func (p *Printer) Start(ctx context.Context, defaultMessage string) {
	p.defaultMessage = defaultMessage
	//p.console.Message(ctx, fmt.Sprintf("Start: %s", defaultMessage))
	p.console.ShowSpinner(ctx, defaultMessage, input.Step)
}

func (p *Printer) Progress(ctx context.Context, status string) {
	progressMessage := fmt.Sprintf("%s (%s)", p.defaultMessage, status)
	//p.console.Message(ctx, fmt.Sprintf("Progress: %s", progressMessage))
	p.console.ShowSpinner(ctx, progressMessage, input.Step)
}

func (p *Printer) Done(ctx context.Context) {
	//p.console.Message(ctx, fmt.Sprintf("Done: %s", p.defaultMessage))
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepDone)
	p.defaultMessage = ""
}

func (p *Printer) Fail(ctx context.Context) {
	//p.console.Message(ctx, fmt.Sprintf("Fail: %s", p.defaultMessage))
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepFailed)
	p.defaultMessage = ""
}

func (p *Printer) Warn(ctx context.Context) {
	//p.console.Message(ctx, fmt.Sprintf("Warn: %s", p.defaultMessage))
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepWarning)
	p.defaultMessage = ""
}

func (p *Printer) Skip(ctx context.Context) {
	//p.console.Message(ctx, fmt.Sprintf("Skip: %s", p.defaultMessage))
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepSkipped)
	p.defaultMessage = ""
}

func (p *Printer) writeMessage(ctx context.Context, msg *messaging.Message) {
	switch msg.Type {
	case project.ProgressMessageKind:
		statusMessage := msg.Value.(string)
		p.Progress(ctx, statusMessage)
	case ext.HookMessageKind:
		hookMsg, ok := msg.Value.(*ext.HookMessage)
		if !ok {
			return
		}

		commandHookMsg := fmt.Sprintf("Executing '%s' command hook", hookMsg.Config.Name)
		serviceHookMsg := fmt.Sprintf("Executing '%s' service hook", hookMsg.Config.Name)

		switch hookMsg.State {
		case ext.StateInProgress:
			if p.defaultMessage == "" {
				p.Start(ctx, commandHookMsg)
			} else {
				p.Progress(ctx, serviceHookMsg)
			}
		case ext.StateCompleted:
			if p.defaultMessage == commandHookMsg {
				p.Done(ctx)
			}
		case ext.StateWarning:
			if p.defaultMessage == commandHookMsg {
				p.Warn(ctx)
			}
		case ext.StateFailed:
			if p.defaultMessage == commandHookMsg {
				p.Fail(ctx)
			}
		}
	}
}
