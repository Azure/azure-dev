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
	Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error
	Close() error
}

type frameworkServiceRegistrar interface {
	Register(ctx context.Context, factory FrameworkServiceFactory, language string) error
	Close() error
}

type extensionEventManager interface {
	AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error
	AddServiceEventHandler(
		ctx context.Context, eventName string, handler ServiceEventHandler, options *ServiceEventOptions,
	) error
	Receive(ctx context.Context) error
	Close() error
}

// ServiceTargetRegistration describes a service target provider to register with azd core.
type ServiceTargetRegistration struct {
	Host    string
	Factory func() ServiceTargetProvider
}

// FrameworkServiceRegistration describes a framework service provider to register with azd core.
type FrameworkServiceRegistration struct {
	Language string
	Factory  func() FrameworkServiceProvider
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
	Options   *ServiceEventOptions
}

// ProviderFactory describes a function that creates a provider instance
type ProviderFactory[T any] func() T

// ProviderFactory describes a function that creates an instance of a service target provider
type ServiceTargetFactory ProviderFactory[ServiceTargetProvider]

// FrameworkServiceFactory describes a function that creates an instance of a framework service provider
type FrameworkServiceFactory ProviderFactory[FrameworkServiceProvider]

// ExtensionHost coordinates registering service targets, wiring event handlers, and signaling readiness.
type ExtensionHost struct {
	client *AzdClient

	serviceTargets    []ServiceTargetRegistration
	frameworkServices []FrameworkServiceRegistration
	projectHandlers   []ProjectEventRegistration
	serviceHandlers   []ServiceEventRegistration

	newServiceTargetManager    func(*AzdClient) serviceTargetRegistrar
	newFrameworkServiceManager func(*AzdClient) frameworkServiceRegistrar
	newEventManager            func(*AzdClient) extensionEventManager
	readyFn                    func(context.Context) error
}

// NewExtensionHost creates a new ExtensionHost for the supplied azd client.
func NewExtensionHost(client *AzdClient) *ExtensionHost {
	return &ExtensionHost{
		client: client,
		newServiceTargetManager: func(c *AzdClient) serviceTargetRegistrar {
			return NewServiceTargetManager(c)
		},
		newFrameworkServiceManager: func(c *AzdClient) frameworkServiceRegistrar {
			return NewFrameworkServiceManager(c)
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
func (er *ExtensionHost) WithServiceTarget(host string, factory ServiceTargetFactory) *ExtensionHost {
	er.serviceTargets = append(er.serviceTargets, ServiceTargetRegistration{Host: host, Factory: factory})
	return er
}

// WithFrameworkService registers a framework service provider to be wired when Run is invoked.
func (er *ExtensionHost) WithFrameworkService(language string, factory FrameworkServiceFactory) *ExtensionHost {
	er.frameworkServices = append(er.frameworkServices, FrameworkServiceRegistration{Language: language, Factory: factory})
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
	options *ServiceEventOptions,
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
	var frameworkManagers []frameworkServiceRegistrar

	for _, reg := range er.serviceTargets {
		if reg.Factory == nil {
			return fmt.Errorf("service target provider for host '%s' is nil", reg.Host)
		}

		manager := er.newServiceTargetManager(er.client)
		if err := manager.Register(ctx, reg.Factory, reg.Host); err != nil {
			_ = manager.Close()

			for _, registered := range serviceManagers {
				_ = registered.Close()
			}

			return fmt.Errorf("failed to register service target '%s': %w", reg.Host, err)
		}

		serviceManagers = append(serviceManagers, manager)
	}

	for _, reg := range er.frameworkServices {
		if reg.Factory == nil {
			return fmt.Errorf("framework service provider for language '%s' is nil", reg.Language)
		}

		manager := er.newFrameworkServiceManager(er.client)
		if err := manager.Register(ctx, reg.Factory, reg.Language); err != nil {
			_ = manager.Close()

			for _, registered := range frameworkManagers {
				_ = registered.Close()
			}
			for _, registered := range serviceManagers {
				_ = registered.Close()
			}

			return fmt.Errorf("failed to register framework service '%s': %w", reg.Language, err)
		}

		frameworkManagers = append(frameworkManagers, manager)
	}

	if len(serviceManagers) > 0 {
		defer func() {
			for i := len(serviceManagers) - 1; i >= 0; i-- {
				_ = serviceManagers[i].Close()
			}
		}()
	}

	if len(frameworkManagers) > 0 {
		defer func() {
			for i := len(frameworkManagers) - 1; i >= 0; i-- {
				_ = frameworkManagers[i].Close()
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
