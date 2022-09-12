package ux

import (
	"context"
)

// Creates a new instance of an ActionPrinter
func NewActionPrinter[R any]() *ActionPrinter[R] {
	return &ActionPrinter[R]{
		continueOnError: false,
		initStep:        &initStep{},
		progressSteps:   []Step{},
		completeStep:    &completeStep{},
		stepContext:     NewStepContext(),
	}
}

// Manages a multi-step command interaction with interactive console
type ActionPrinter[R any] struct {
	initStep        *initStep
	progressSteps   []Step
	completeStep    *completeStep
	continueOnError bool
	stepContext     StepContext
}

// Sets whether additional steps should be run after an initial error is encountered
func (p *ActionPrinter[R]) ContinueOnError(value bool) *ActionPrinter[R] {
	p.continueOnError = value
	return p
}

// Sets the initial title that will be printed to the console
func (p *ActionPrinter[R]) Title(title string) *ActionPrinter[R] {
	p.initStep.title = title
	return p
}

// Sets the initial action description that will be printed to the console.
func (p *ActionPrinter[R]) Description(description string) *ActionPrinter[R] {
	p.initStep.description = description
	return p
}

func (p *ActionPrinter[R]) AddStep(step Step) *ActionPrinter[R] {
	p.progressSteps = append(p.progressSteps, step)
	return p
}

// Adds a progress step in the overall action orchestration
func (p *ActionPrinter[R]) AddProgressStep(prefix string, message string, execFn ProgressStepFn) *ActionPrinter[R] {
	newStep := NewProgressStep(prefix, message, execFn)
	return p.AddStep(newStep)
}

// Registers the action that will be run on successful execution of the progress steps
func (p *ActionPrinter[R]) Complete(successTitle string, completeFn StepFn) *ActionPrinter[R] {
	p.completeStep.successTitle = successTitle
	p.completeStep.successFn = completeFn
	return p
}

// Registers the action that will be run on unsuccessful execution of the progress steps
func (p *ActionPrinter[R]) Error(errorTitle string, errorFn ErrorFn) *ActionPrinter[R] {
	p.completeStep.errorTitle = errorTitle
	p.completeStep.errorFn = errorFn
	return p
}

// Runs the configured sets of steps for the actions
func (p *ActionPrinter[R]) Run(ctx context.Context) error {
	errors := []error{}
	commandError := p.initStep.Execute(ctx, p.stepContext)

	if commandError == nil {
		for _, step := range p.progressSteps {
			commandError := step.Execute(ctx, p.stepContext)
			if commandError != nil {
				errors = append(errors, commandError)

				if p.continueOnError && step.ContinueOnError() {
					continue
				}

				break
			}
		}
	}

	if len(errors) > 0 {
		p.completeStep.error = errors[0]
	}
	p.completeStep.Execute(ctx, p.stepContext)

	return commandError
}
