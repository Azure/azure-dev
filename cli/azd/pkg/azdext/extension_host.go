// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceReceiver interface {
	Receive(ctx context.Context) error
	Ready(ctx context.Context) error
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
		client: client,
	}
}

func (er *ExtensionHost) init(extensionId string) {
	// Only create managers if they haven't been set (allows tests to inject mocks)
	if er.serviceTargetManager == nil {
		er.serviceTargetManager = NewServiceTargetManager(extensionId, er.client)
	}
	if er.frameworkServiceManager == nil {
		er.frameworkServiceManager = NewFrameworkServiceManager(extensionId, er.client)
	}
	if er.eventManager == nil {
		er.eventManager = NewEventManager(extensionId, er.client)
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
	extensionId := getExtensionId(ctx)
	er.init(extensionId)

	// Wait for debugger if AZD_EXT_DEBUG is set
	// When user declines or cancels, continue so extension doesn't exit while azd continues
	_ = WaitForDebugger(ctx, er.client)

	// Silence the global logger in extension processes to prevent internal
	// gRPC broker trace logs from appearing in stderr. Extensions compiled
	// against older SDK versions still use log.Printf directly, so this
	// ensures backward compatibility. When AZD_EXT_DEBUG is true, keep
	// logging to stderr for diagnostics.
	if os.Getenv("AZD_EXT_DEBUG") != "true" {
		log.SetOutput(io.Discard)
	}

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

	// Collect active receivers and start them BEFORE registration
	// This ensures broker.Run() is active to receive registration responses
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
	// CRITICAL: This must happen BEFORE any Register() calls that use broker.Send()
	var receiveWaitGroup sync.WaitGroup
	receiverErrors := make(chan error, len(receivers))

	for _, receiver := range receivers {
		receiveWaitGroup.Add(1)
		go func(r serviceReceiver) {
			defer receiveWaitGroup.Done()
			if err := r.Receive(ctx); err != nil {
				receiverErrors <- fmt.Errorf("receiver error: %w", err)
			}
		}(receiver)
	}

	// Wait for all active receivers' brokers to signal readiness before starting registrations
	var readyErrors []error
	for _, receiver := range receivers {
		if err := receiver.Ready(ctx); err != nil {
			readyErrors = append(readyErrors, fmt.Errorf("receiver not ready: %w", err))
		}
	}

	// Check if any managers failed to become ready
	if len(readyErrors) > 0 {
		return errors.Join(readyErrors...)
	}

	// Now that receivers are running, perform registrations
	// The broker.Run() in each Receive() will process the registration responses

	// Register all registrations in parallel - service targets, framework services, and event handlers
	var registrationsWaitGroup sync.WaitGroup
	totalCount := len(er.serviceTargets) + len(er.frameworkServices) + len(er.projectHandlers) + len(er.serviceHandlers)
	registrationErrChan := make(chan error, totalCount)

	// Register service targets in parallel
	for _, reg := range er.serviceTargets {
		if reg.Factory == nil {
			return fmt.Errorf("service target provider for host '%s' is nil", reg.Host)
		}

		registrationsWaitGroup.Add(1)
		go func(r ServiceTargetRegistration) {
			defer registrationsWaitGroup.Done()
			if err := er.serviceTargetManager.Register(ctx, r.Factory, r.Host); err != nil {
				registrationErrChan <- fmt.Errorf("failed to register service target '%s': %w", r.Host, err)
			}
		}(reg)
	}

	// Register framework services in parallel
	for _, reg := range er.frameworkServices {
		if reg.Factory == nil {
			return fmt.Errorf("framework service provider for language '%s' is nil", reg.Language)
		}

		registrationsWaitGroup.Add(1)
		go func(r FrameworkServiceRegistration) {
			defer registrationsWaitGroup.Done()
			if err := er.frameworkServiceManager.Register(ctx, r.Factory, r.Language); err != nil {
				registrationErrChan <- fmt.Errorf("failed to register framework service '%s': %w", r.Language, err)
			}
		}(reg)
	}

	// Register project event handlers in parallel
	for _, reg := range er.projectHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("project event handler for '%s' is nil", reg.EventName)
		}

		registrationsWaitGroup.Add(1)
		go func(r ProjectEventRegistration) {
			defer registrationsWaitGroup.Done()
			if err := er.eventManager.AddProjectEventHandler(ctx, r.EventName, r.Handler); err != nil {
				registrationErrChan <- fmt.Errorf("failed to add project event handler '%s': %w", r.EventName, err)
			}
		}(reg)
	}

	// Register service event handlers in parallel
	for _, reg := range er.serviceHandlers {
		if reg.Handler == nil {
			return fmt.Errorf("service event handler for '%s' is nil", reg.EventName)
		}

		registrationsWaitGroup.Add(1)
		go func(r ServiceEventRegistration) {
			defer registrationsWaitGroup.Done()
			if err := er.eventManager.AddServiceEventHandler(ctx, r.EventName, r.Handler, r.Options); err != nil {
				registrationErrChan <- fmt.Errorf("failed to add service event handler '%s': %w", r.EventName, err)
			}
		}(reg)
	}

	// Wait for ALL registrations to complete in parallel
	registrationsWaitGroup.Wait()
	close(registrationErrChan)

	// Check for any registration errors - collect all errors
	var registrationErrors []error
	for err := range registrationErrChan {
		registrationErrors = append(registrationErrors, err)
	}

	if len(registrationErrors) > 0 {
		if len(registrationErrors) == 1 {
			return registrationErrors[0]
		}
		// Multiple errors - combine them using Go's standard error joining
		return errors.Join(registrationErrors...)
	}

	// Signal readiness after all registrations are complete
	if err := callReady(ctx, er.client); err != nil {
		return err
	}

	// If no receivers, just return after signaling ready
	if len(receivers) == 0 {
		return nil
	}

	// Wait for all receivers to complete and monitor for errors
	receiversDone := make(chan struct{})
	go func() {
		receiveWaitGroup.Wait()
		close(receiverErrors)
		close(receiversDone)
	}()

	// Block until context cancellation, receiver error, or receivers complete
	select {
	case <-ctx.Done():
		log.Println("Extension host context cancelled, shutting down")
		return nil // Context cancellation is expected, not an error
	case err := <-receiverErrors:
		if err != nil {
			return err
		}
		// Continue waiting for more errors or completion
		return nil
	case <-receiversDone:
		// All receivers completed normally
		return nil
	}
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
