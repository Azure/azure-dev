package ux

import (
	"context"
	"fmt"

	"github.com/fatih/color"
)

type initStep struct {
	title       string
	description string
}

func (s *initStep) Execute(ctx context.Context, stepCtx StepContext) error {
	c := color.New(color.FgWhite).Add(color.Bold)
	c.Println(s.title)

	if s.description != "" {
		fmt.Println(s.description)
	}

	fmt.Println()

	return nil
}

func (s *initStep) ContinueOnError() bool {
	return true
}

func NewInitStep(title string, description string) Step {
	return &initStep{
		title:       title,
		description: description,
	}
}
