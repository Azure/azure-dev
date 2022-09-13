package ux

import (
	"context"
	"fmt"

	"github.com/fatih/color"
)

type ErrorFn func(ctx context.Context, stepCtx StepContext, err error)

type completeStep struct {
	// Success
	successTitle string
	successFn    StepFn
	// Error
	errorTitle string
	errorFn    ErrorFn
	error      error
}

// Gets a value specifying whether or not the step should fail the whole action
func (s *completeStep) ContinueOnError() bool {
	return false
}

func (s *completeStep) Execute(ctx context.Context, stepCtx StepContext) error {
	fmt.Println()

	if s.error == nil {
		boldGreen := color.New(color.FgGreen).Add(color.Bold)
		boldGreen.Print("SUCCESS: ")
		color.Green(s.successTitle)
		s.successFn(ctx, stepCtx)

	} else {
		boldRed := color.New(color.FgRed).Add(color.Bold)
		boldRed.Print("ERROR: ")
		color.Red(s.errorTitle)
		s.errorFn(ctx, stepCtx, s.error)
	}

	fmt.Println()

	return nil
}
