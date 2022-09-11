package ux

import (
	"context"
	"fmt"

	"github.com/fatih/color"
)

type CompleteFn[R any] func(ctx context.Context, result R) error

type completeStep[R any] struct {
	message    string
	completeFn CompleteFn[R]
	err        error
	result     R
}

func (s *completeStep[R]) Result(value R) {
	s.result = value
}

func (s *completeStep[R]) Execute(ctx context.Context) (R, error) {
	fmt.Println()

	if s.err == nil {
		boldGreen := color.New(color.FgGreen).Add(color.Bold)
		boldGreen.Print("SUCCESS: ")
		color.Green(s.message)
	} else {
		boldRed := color.New(color.FgRed).Add(color.Bold)
		boldRed.Print("ERROR: ")
		color.Red(s.err.Error())
	}

	s.completeFn(ctx, s.result)

	return s.result, nil
}

func NewCompleteStep[R any](successMessage string, completeFn CompleteFn[R]) Step[R] {
	return &completeStep[R]{
		message:    successMessage,
		completeFn: completeFn,
	}
}
