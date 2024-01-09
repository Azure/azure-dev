package ioc

import (
	"context"
	"errors"
)

type iocContainerKeyType string

const containerKey iocContainerKeyType = "ioc-container"

func WithContainer(ctx context.Context, container *NestedContainer) context.Context {
	return context.WithValue(ctx, containerKey, container)
}

func GetContainer(ctx context.Context) (*NestedContainer, error) {
	container, ok := ctx.Value(containerKey).(*NestedContainer)
	if !ok {
		return nil, errors.New("container not found in context")
	}

	return container, nil
}
