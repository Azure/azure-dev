package ioc

import (
	"context"
	"errors"
)

type iocContainerKeyType string

const containerKey iocContainerKeyType = "ioc-container"

// WithContainer adds a container to the context and returns a new context
func WithContainer(ctx context.Context, container *NestedContainer) context.Context {
	return context.WithValue(ctx, containerKey, container)
}

// GetContainer returns the container from the context
// If the container is not found in the context then an error is returned
func GetContainer(ctx context.Context) (*NestedContainer, error) {
	container, ok := ctx.Value(containerKey).(*NestedContainer)
	if !ok {
		return nil, errors.New("container not found in context")
	}

	return container, nil
}
