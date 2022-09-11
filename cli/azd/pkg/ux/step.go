package ux

import (
	"context"
)

type ExecuteFn[R any] func(ctx context.Context, progress *Progress) (R, error)

type Step[R any] interface {
	Execute(ctx context.Context) (R, error)
}
