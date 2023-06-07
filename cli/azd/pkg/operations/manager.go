package operations

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
)

type messageManager struct {
	publisher messaging.Publisher
}

func NewMessageManager(publisher messaging.Publisher) Manager {
	return &messageManager{
		publisher: publisher,
	}
}

func (om *messageManager) Send(ctx context.Context, message *Message) error {
	envelope := messaging.NewEnvelope(defaultMessageKind, message)
	return om.publisher.Send(ctx, envelope)
}

func (om *messageManager) ReportProgress(ctx context.Context, progressMessage string) {
	envelope, _ := NewMessage(progressMessage, StateProgress)
	if err := om.publisher.Send(ctx, envelope); err != nil {
		log.Printf("failed sending progress message: %s", err.Error())
	}
}

func (om *messageManager) Run(ctx context.Context, operationMessage string, operationFunc OperationRunFunc) error {
	operation := newOperation(om)

	envelope, _ := NewCorrelatedMessage(operation.correlationId, operationMessage, StateRunning)
	if err := om.publisher.Send(ctx, envelope); err != nil {
		log.Printf("failed sending start message: %s", err.Error())
	}

	if err := operationFunc(operation); err != nil {
		envelope, _ := NewCorrelatedMessage(operation.correlationId, operationMessage, StateError)
		if err := om.publisher.Send(ctx, envelope); err != nil {
			log.Printf("failed sending error message: %s", err.Error())
		}

		return err
	}

	envelope, _ = NewCorrelatedMessage(operation.correlationId, operationMessage, StateSuccess)
	if err := om.publisher.Send(ctx, envelope); err != nil {
		log.Printf("failed sending success message: %s", err.Error())
	}

	return nil
}
