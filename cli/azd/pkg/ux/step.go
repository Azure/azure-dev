package ux

import (
	"context"
)

type StepFn func(ctx context.Context, stepCtx StepContext) error
type ProgressStepFn func(ctx context.Context, stepCtx StepContext, progress *Progress) error

// A UX step with consistent user experience
type Step interface {
	// The action to perform during the step
	Execute(ctx context.Context, stepCtx StepContext) error
	// Whether or not the step should fail the whole action
	ContinueOnError() bool
}
