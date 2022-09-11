package ux

import (
	"context"
	"time"

	"github.com/theckman/yacspin"
)

type progressStep[R any] struct {
	progress  *Progress
	spinner   *yacspin.Spinner
	executeFn ExecuteFn[R]
}

func (s *progressStep[R]) Execute(ctx context.Context) (R, error) {
	var result R
	s.spinner.Start()

	result, err := s.executeFn(ctx, s.progress)
	if err != nil {
		s.spinner.StopFail()
		return result, err
	}

	s.spinner.Stop()
	return result, nil
}

func NewProgressStep[R any](prefix string, message string, executeFn ExecuteFn[R]) Step[R] {
	config := yacspin.Config{
		Frequency:         200 * time.Millisecond,
		CharSet:           yacspin.CharSets[33],
		Suffix:            " " + prefix,
		Message:           message,
		SuffixAutoColon:   true,
		StopCharacter:     "(✓) Done",
		StopColors:        []string{"fgGreen"},
		StopFailCharacter: "(x) Error",
		StopFailColors:    []string{"fgRed"},
	}

	spinner, _ := yacspin.New(config)
	progress := &Progress{
		spinner: spinner,
	}

	return &progressStep[R]{
		progress:  progress,
		spinner:   spinner,
		executeFn: executeFn,
	}
}
