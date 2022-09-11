package ux

import (
	"context"
)

func NewCommandPrinter[R any]() *CommandPrinter[R] {
	return &CommandPrinter[R]{
		continueOnError: false,
		initStep:        &initStep{},
		progressSteps:   []Step[R]{},
		completeStep:    &completeStep[R]{},
	}
}

type CommandPrinter[R any] struct {
	initStep        *initStep
	progressSteps   []Step[R]
	completeStep    *completeStep[R]
	continueOnError bool
}

func (p *CommandPrinter[R]) ContinueOnError(value bool) *CommandPrinter[R] {
	p.continueOnError = value
	return p
}

func (p *CommandPrinter[R]) Title(title string) *CommandPrinter[R] {
	p.initStep.title = title
	return p
}

func (p *CommandPrinter[R]) Description(description string) *CommandPrinter[R] {
	p.initStep.description = description
	return p
}

func (p *CommandPrinter[R]) AddStep(prefix string, message string, execFn ExecuteFn[R]) *CommandPrinter[R] {
	newStep := NewProgressStep(prefix, message, execFn)
	p.progressSteps = append(p.progressSteps, newStep)
	return p
}

func (p *CommandPrinter[R]) Complete(message string, completeFn CompleteFn[R]) *CommandPrinter[R] {
	p.completeStep.message = message
	p.completeStep.completeFn = completeFn
	return p
}

func (p *CommandPrinter[R]) Run(ctx context.Context) error {
	_, commandError := p.initStep.Execute(ctx)

	if commandError == nil {
		for _, step := range p.progressSteps {
			result, commandError := step.Execute(ctx)
			if commandError != nil && !p.continueOnError {
				break
			}
			p.completeStep.Result(result)
		}
	}

	p.completeStep.err = commandError
	p.completeStep.Execute(ctx)

	return commandError
}
