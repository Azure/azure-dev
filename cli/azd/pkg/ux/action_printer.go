package ux

import (
	"context"
	"io"
)

// Creates a new instance of an ActionPrinter
func NewActionPrinter(writer io.Writer) *ActionPrinter {
	return &ActionPrinter{
		continueOnError: false,
		initStep:        &initStep{},
		progressSteps:   []Step{},
		completeStep:    &completeStep{},
		stepContext:     NewStepContext(),
		indentation:     "    ",
	}
}

// Manages a multi-step command interaction with interactive console
type ActionPrinter struct {
	initStep        *initStep
	progressSteps   []Step
	completeStep    *completeStep
	continueOnError bool
	stepContext     StepContext
	indentation     string
}

// Sets the indentation for command output
func (p *ActionPrinter) Indent(value string) *ActionPrinter {
	p.indentation = value
	return p
}

// Sets whether additional steps should be run after an initial error is encountered
func (p *ActionPrinter) ContinueOnError(value bool) *ActionPrinter {
	p.continueOnError = value
	return p
}

// Sets the initial title that will be printed to the console
func (p *ActionPrinter) Title(title string) *ActionPrinter {
	p.initStep.title = title
	return p
}

// Sets the initial action description that will be printed to the console.
func (p *ActionPrinter) Description(description string) *ActionPrinter {
	p.initStep.description = description
	return p
}

// Adds a progress step in the overall action orchestration
func (p *ActionPrinter) AddStep(step Step) *ActionPrinter {
	step.SetIndent(p.indentation)

	p.progressSteps = append(p.progressSteps, step)
	return p
}

// Registers the action that will be run on successful execution of the progress steps
func (p *ActionPrinter) Complete(successTitle string, completeFn StepFn) *ActionPrinter {
	p.completeStep.successTitle = successTitle
	p.completeStep.successFn = completeFn
	return p
}

// Registers the action that will be run on unsuccessful execution of the progress steps
func (p *ActionPrinter) Error(errorTitle string, errorFn ErrorFn) *ActionPrinter {
	p.completeStep.errorTitle = errorTitle
	p.completeStep.errorFn = errorFn
	return p
}

// Runs the configured sets of steps for the actions
func (p *ActionPrinter) Run(ctx context.Context) error {
	errors := []error{}
	commandError := p.initStep.Execute(ctx, p.stepContext)

	// TODO: Whether or not we want to set indentation on console
	// console := input.NewConsole(true, output.GetDefaultWriter(), output.GetFormatter(ctx))
	// console.SetIndentation(p.indentation)
	// ctx = input.WithConsole(ctx, console)

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
