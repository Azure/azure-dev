// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceReceiver interface {
	Receive(ctx context.Context) error
}

type serviceTargetRegistrar interface {
	serviceReceiver
	Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error
	Close() error
}

type frameworkServiceRegistrar interface {
	serviceReceiver
	Register(ctx context.Context, factory FrameworkServiceFactory, language string) error
	Close() error
}

type extensionEventManager interface {
	serviceReceiver
	AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error
	AddServiceEventHandler(
		ctx context.Context, eventName string, handler ServiceEventHandler, options *ServiceEventOptions,
	) error
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

	serviceTargetManager    serviceTargetRegistrar
	frameworkServiceManager frameworkServiceRegistrar
	eventManager            extensionEventManager
}

// NewExtensionHost creates a new ExtensionHost for the supplied azd client.
func NewExtensionHost(client *AzdClient) *ExtensionHost {
	return &ExtensionHost{
		client:                  client,
		serviceTargetManager:    NewServiceTargetManager(client),
		frameworkServiceManager: NewFrameworkServiceManager(client),
		eventManager:            NewEventManager(client),
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
	// Wait for debugger if AZD_EXT_DEBUG is set
	waitForDebugger(ctx, er.client)

	// Determine which managers will be active
	hasServiceTargets := len(er.serviceTargets) > 0
	hasFrameworkServices := len(er.frameworkServices) > 0
	hasEventHandlers := len(er.projectHandlers) > 0 || len(er.serviceHandlers) > 0

	// Set up defer for cleanup
	defer func() {
		if hasServiceTargets {
			_ = er.serviceTargetManager.Close()
		}
		if hasFrameworkServices {
			_ = er.frameworkServiceManager.Close()
		}
		if hasEventHandlers {
			_ = er.eventManager.Close()
		}
	}()

	// Register all service targets with the single manager
	for _, reg := range er.serviceTargets {
		if reg.Factory == nil {
			return fmt.Errorf("service target provider for host '%s' is nil", reg.Host)
		}

		if err := er.serviceTargetManager.Register(ctx, reg.Factory, reg.Host); err != nil {
			return fmt.Errorf("failed to register service target '%s': %w", reg.Host, err)
		}
	}

	// Register all framework services with the single manager
	for _, reg := range er.frameworkServices {
		if reg.Factory == nil {
			return fmt.Errorf("framework service provider for language '%s' is nil", reg.Language)
		}

		if err := er.frameworkServiceManager.Register(ctx, reg.Factory, reg.Language); err != nil {
			return fmt.Errorf("failed to register framework service '%s': %w", reg.Language, err)
		}
	}

	// Register all event handlers with the single manager
	for _, reg := range er.projectHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("project event handler for '%s' is nil", reg.EventName)
		}

		if err := er.eventManager.AddProjectEventHandler(ctx, reg.EventName, reg.Handler); err != nil {
			return fmt.Errorf("failed to add project event handler '%s': %w", reg.EventName, err)
		}
	}

	for _, reg := range er.serviceHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("service event handler for '%s' is nil", reg.EventName)
		}

		if err := er.eventManager.AddServiceEventHandler(ctx, reg.EventName, reg.Handler, reg.Options); err != nil {
			return fmt.Errorf("failed to add service event handler '%s': %w", reg.EventName, err)
		}
	}

	// Collect active receivers
	receivers := []serviceReceiver{}
	if hasServiceTargets {
		receivers = append(receivers, er.serviceTargetManager)
	}
	if hasFrameworkServices {
		receivers = append(receivers, er.frameworkServiceManager)
	}
	if hasEventHandlers {
		receivers = append(receivers, er.eventManager)
	}

	// Start receiving messages from active managers in separate goroutines
	var wg sync.WaitGroup
	errChan := make(chan error, len(receivers))

	for _, receiver := range receivers {
		wg.Add(1)
		go func(r serviceReceiver) {
			defer wg.Done()
			if err := r.Receive(ctx); err != nil {
				errChan <- fmt.Errorf("receiver error: %w", err)
			}
		}(receiver)
	}

	// Signal readiness after all registrations are complete
	if err := callReady(ctx, er.client); err != nil {
		return err
	}

	// If no receivers, just return after signaling ready
	if len(receivers) == 0 {
		return nil
	}

	// Wait for all receivers to complete or first error
	go func() {
		wg.Wait()
		close(errChan)
	}()

	// Block until context cancellation or first error
	select {
	case <-ctx.Done():
		log.Println("Extension host context cancelled, shutting down")
		return nil // Context cancellation is expected, not an error
	case err := <-errChan:
		if err != nil {
			return err
		}
	}

	return nil
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
