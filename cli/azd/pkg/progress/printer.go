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
	progressSubscription := p.subscriber.Subscribe(ctx, progressFilter, func(msg *messaging.Message) {
		msg.Tags["handled"] = struct{}{}

		switch msg.Type {
		case project.ProgressMessageKind:
			statusMessage := msg.Value.(string)
			p.Progress(ctx, statusMessage)
		case ext.HookMessageKind:
			hookMsg, ok := msg.Value.(*ext.HookMessage)
			if !ok {
				return
			}

			hookMessage := fmt.Sprintf("Executing '%s' hook", hookMsg.Config.Name)

			switch hookMsg.State {
			case ext.StateInProgress:
				if p.defaultMessage == "" {
					p.Start(ctx, hookMessage)
				} else {
					p.Progress(ctx, hookMessage)
				}
			case ext.StateCompleted:
				if p.defaultMessage == hookMessage {
					p.Done(ctx)
				}
			case ext.StateWarning:
				if p.defaultMessage == hookMessage {
					p.Warn(ctx)
				}
			case ext.StateFailed:
				if p.defaultMessage == hookMessage {
					p.Fail(ctx)
				}
			}
		}
	})

	return progressSubscription
}

func (p *Printer) Start(ctx context.Context, defaultMessage string) {
	p.defaultMessage = defaultMessage
	p.console.ShowSpinner(ctx, defaultMessage, input.Step)
}

func (p *Printer) Progress(ctx context.Context, status string) {
	progressMessage := fmt.Sprintf("%s (%s)", p.defaultMessage, status)
	p.console.ShowSpinner(ctx, progressMessage, input.Step)
}

func (p *Printer) Done(ctx context.Context) {
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepDone)
}

func (p *Printer) Fail(ctx context.Context) {
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepFailed)
}

func (p *Printer) Warn(ctx context.Context) {
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepWarning)
}

func (p *Printer) Skip(ctx context.Context) {
	p.console.StopSpinner(ctx, p.defaultMessage, input.StepSkipped)
}
