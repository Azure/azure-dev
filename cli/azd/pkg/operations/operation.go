package operations

import (
	"context"
	"log"

	"github.com/google/uuid"
)

// Operation represent an atomic long running operation
type Operation struct {
	correlationId    uuid.UUID
	operationManager Manager
}

// NewOperation creates a new operation
func newOperation(operationManager Manager) *Operation {
	return &Operation{
		correlationId:    uuid.New(),
		operationManager: operationManager,
	}
}

// Succeed reports the operation as successful
func (o *Operation) Succeed(ctx context.Context, message string) {
	_, msg := NewCorrelatedMessage(o.correlationId, message, StateSuccess)
	if err := o.operationManager.Send(ctx, msg); err != nil {
		log.Printf("failed sending success message: %s", err.Error())
	}
}

// Progress reports the operation as in progress
func (o *Operation) Progress(ctx context.Context, message string) {
	_, msg := NewCorrelatedMessage(o.correlationId, message, StateProgress)
	if err := o.operationManager.Send(ctx, msg); err != nil {
		log.Printf("failed sending progress message: %s", err.Error())
	}
}

// Fail reports the operation as failed
func (o *Operation) Fail(ctx context.Context, message string) {
	_, msg := NewCorrelatedMessage(o.correlationId, message, StateError)
	if err := o.operationManager.Send(ctx, msg); err != nil {
		log.Printf("failed sending error message: %s", err.Error())
	}
}

// Skip reports the operation as skipped
func (o *Operation) Skip(ctx context.Context) {
	_, msg := NewCorrelatedMessage(o.correlationId, "skipped", StateSkipped)
	if err := o.operationManager.Send(ctx, msg); err != nil {
		log.Printf("failed sending skip message: %s", err.Error())
	}
}

// Warn reports the operation has a warning
func (o *Operation) Warn(ctx context.Context, message string) {
	_, msg := NewCorrelatedMessage(o.correlationId, message, StateWarning)
	if err := o.operationManager.Send(ctx, msg); err != nil {
		log.Printf("failed sending warning message: %s", err.Error())
	}
}
