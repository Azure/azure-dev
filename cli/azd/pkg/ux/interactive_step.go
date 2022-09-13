package ux

import (
	"context"
	"fmt"
)

type interactiveStep struct {
	stepFn StepFn
	indent string
}

func NewInteractiveStep(stepFn StepFn) Step {
	return &interactiveStep{
		stepFn: stepFn,
	}
}

func (s *interactiveStep) SetIndent(value string) {
	s.indent = value
}

func (s *interactiveStep) ContinueOnError() bool {
	return false
}

func (s *interactiveStep) Execute(ctx context.Context, stepCtx StepContext) error {
	fmt.Println()
	return s.stepFn(ctx, stepCtx)
}
