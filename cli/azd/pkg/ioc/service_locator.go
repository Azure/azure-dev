package ioc

import (
	"context"
)

type ServiceLocator interface {
	Resolve(ctx context.Context, instance any) error
	ResolveNamed(ctx context.Context, name string, instance any) error
	Call(ctx context.Context, function interface{}) error
	Fill(ctx context.Context, structure interface{}) error
}
