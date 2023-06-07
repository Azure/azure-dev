package operations

import "context"

type OperationRunFunc func(operation *Operation) error

// Manager orchestrates running operations and sending progress updates
type Manager interface {
	// ReportProgress sends a progress update message
	ReportProgress(ctx context.Context, message string)

	// Run executes an operation and sends running, success, or error messages
	Run(ctx context.Context, operationMessage string, operationFunc OperationRunFunc) error

	// Send sends a generic operation message
	Send(ctx context.Context, message *Message) error
}

// Printers orchestrates rendering operation updates in the UX CLI
type Printer interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Flush(ctx context.Context) error
}
