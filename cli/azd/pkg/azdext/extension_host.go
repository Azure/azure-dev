// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceTargetRegistrar interface {
	Register(ctx context.Context, provider ServiceTargetProvider, hostType string) error
	Close() error
}

type extensionEventManager interface {
	AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error
	AddServiceEventHandler(
		ctx context.Context, eventName string, handler ServiceEventHandler, options *ServerEventOptions,
	) error
	Receive(ctx context.Context) error
	Close() error
}

// ServiceTargetRegistration describes a service target provider to register with azd core.
type ServiceTargetRegistration struct {
	Host     string
	Provider ServiceTargetProvider
}

// ProjectEventRegistration describes a project-level event handler to register.
type ProjectEventRegistration struct {
	EventName string
	Handler   ProjectEventHandler
}

// ServiceEventRegistration describes a service-level event handler to register.
type ServiceEventRegistration struct {
	EventName string
	Handler   ServiceEventHandler
	Options   *ServerEventOptions
}

// ExtensionHost coordinates registering service targets, wiring event handlers, and signaling readiness.
type ExtensionHost struct {
	client *AzdClient

	serviceTargets  []ServiceTargetRegistration
	projectHandlers []ProjectEventRegistration
	serviceHandlers []ServiceEventRegistration

	newServiceTargetManager func(*AzdClient) serviceTargetRegistrar
	newEventManager         func(*AzdClient) extensionEventManager
	readyFn                 func(context.Context) error
}

// NewExtensionHost creates a new ExtensionHost for the supplied azd client.
func NewExtensionHost(client *AzdClient) *ExtensionHost {
	return &ExtensionHost{
		client: client,
		newServiceTargetManager: func(c *AzdClient) serviceTargetRegistrar {
			return NewServiceTargetManager(c)
		},
		newEventManager: func(c *AzdClient) extensionEventManager {
			return NewEventManager(c)
		},
		readyFn: func(ctx context.Context) error {
			return callReady(ctx, client)
		},
	}
}

// WithServiceTarget registers a service target provider to be wired when Run is invoked.
func (er *ExtensionHost) WithServiceTarget(host string, provider ServiceTargetProvider) *ExtensionHost {
	er.serviceTargets = append(er.serviceTargets, ServiceTargetRegistration{Host: host, Provider: provider})
	return er
}

// WithProjectEventHandler registers a project-level event handler to be wired when Run is invoked.
func (er *ExtensionHost) WithProjectEventHandler(eventName string, handler ProjectEventHandler) *ExtensionHost {
	er.projectHandlers = append(er.projectHandlers, ProjectEventRegistration{EventName: eventName, Handler: handler})
	return er
}

// WithServiceEventHandler registers a service-level event handler to be wired when Run is invoked.
func (er *ExtensionHost) WithServiceEventHandler(
	eventName string,
	handler ServiceEventHandler,
	options *ServerEventOptions,
) *ExtensionHost {
	er.serviceHandlers = append(er.serviceHandlers, ServiceEventRegistration{
		EventName: eventName,
		Handler:   handler,
		Options:   options,
	})
	return er
}

// Run wires the configured service targets and event handlers, signals readiness, and blocks until shutdown.
func (er *ExtensionHost) Run(ctx context.Context) error {
	var serviceManagers []serviceTargetRegistrar

	for _, reg := range er.serviceTargets {
		if reg.Provider == nil {
			return fmt.Errorf("service target provider for host '%s' is nil", reg.Host)
		}

		manager := er.newServiceTargetManager(er.client)
		if err := manager.Register(ctx, reg.Provider, reg.Host); err != nil {
			_ = manager.Close()

			for _, registered := range serviceManagers {
				_ = registered.Close()
			}

			return fmt.Errorf("failed to register service target '%s': %w", reg.Host, err)
		}

		serviceManagers = append(serviceManagers, manager)
	}

	if len(serviceManagers) > 0 {
		defer func() {
			for i := len(serviceManagers) - 1; i >= 0; i-- {
				_ = serviceManagers[i].Close()
			}
		}()
	}

	if len(er.projectHandlers) == 0 && len(er.serviceHandlers) == 0 {
		return er.readyFn(ctx)
	}

	eventManager := er.newEventManager(er.client)
	defer eventManager.Close()

	for _, reg := range er.projectHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("project event handler for '%s' is nil", reg.EventName)
		}

		if err := eventManager.AddProjectEventHandler(ctx, reg.EventName, reg.Handler); err != nil {
			return fmt.Errorf("failed to add project event handler '%s': %w", reg.EventName, err)
		}
	}

	for _, reg := range er.serviceHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("service event handler for '%s' is nil", reg.EventName)
		}

		if err := eventManager.AddServiceEventHandler(ctx, reg.EventName, reg.Handler, reg.Options); err != nil {
			return fmt.Errorf("failed to add service event handler '%s': %w", reg.EventName, err)
		}
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return eventManager.Receive(groupCtx)
	})

	group.Go(func() error {
		return er.readyFn(groupCtx)
	})

	return group.Wait()
}

func callReady(ctx context.Context, client *AzdClient) error {
	_, err := client.extensionService().Ready(ctx, &ReadyRequest{})
	if err == nil {
		return nil
	}

	switch status.Code(err) {
	case codes.Canceled, codes.Unavailable:
		return nil
	default:
		return fmt.Errorf("failed to signal readiness (status=%s): %w", status.Code(err), err)
	}
}
