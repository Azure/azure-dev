// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/sync/errgroup"
)

// TaskState represents the current state of a service deployment task
type TaskState int

const (
	StatePending TaskState = iota
	StatePackaging
	StatePublishing
	StateDeploying
	StateComplete
	StateError
)

func (s TaskState) String() string {
	switch s {
	case StatePending:
		return "Pending"
	case StatePackaging:
		return "Packaging"
	case StatePublishing:
		return "Publishing"
	case StateDeploying:
		return "Deploying"
	case StateComplete:
		return "Complete"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// ServiceTask represents a single service deployment task with progress tracking
type ServiceTask struct {
	ServiceName string
	ProgressBar *mpb.Bar
	State       TaskState
	Error       error
	mu          sync.Mutex
}

// UpdateState updates the task state and progress bar label
func (t *ServiceTask) UpdateState(state TaskState, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = state
	// Progress bar decorators are updated automatically via the Any decorator
}

// SetError marks the task as errored
func (t *ServiceTask) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = StateError
	t.Error = err
}

// GetState returns the current state (thread-safe)
func (t *ServiceTask) GetState() TaskState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.State
}

// ParallelDeploymentManager manages parallel deployment of services with progress tracking
type ParallelDeploymentManager struct {
	serviceManager *project.ServiceManager
	maxParallel    int
}

// NewParallelDeploymentManager creates a new parallel deployment manager
func NewParallelDeploymentManager(serviceManager *project.ServiceManager, maxParallel int) *ParallelDeploymentManager {
	if maxParallel <= 0 {
		maxParallel = runtime.NumCPU()
	}
	return &ParallelDeploymentManager{
		serviceManager: serviceManager,
		maxParallel:    maxParallel,
	}
}

// DeployServices deploys multiple services in parallel with progress tracking
func (m *ParallelDeploymentManager) DeployServices(
	ctx context.Context,
	serviceConfigs []*project.ServiceConfig,
) (map[string]*project.ServiceDeployResult, error) {
	if len(serviceConfigs) == 0 {
		return make(map[string]*project.ServiceDeployResult), nil
	}

	// Create progress container
	p := mpb.NewWithContext(ctx,
		mpb.WithWidth(80),
		mpb.WithAutoRefresh(),
	)

	// Create tasks and progress bars for each service
	tasks := make([]*ServiceTask, len(serviceConfigs))
	for i, svc := range serviceConfigs {
		task := &ServiceTask{
			ServiceName: svc.Name,
			State:       StatePending,
		}

		// Create progress bar with state decorator
		bar := p.AddBar(100,
			mpb.PrependDecorators(
				// Service name with fixed width for alignment
				decor.Name(svc.Name, decor.WC{W: 15}),
				// Dynamic state display
				decor.Any(func(decor.Statistics) string {
					state := task.GetState()
					if state == StateError {
						return fmt.Sprintf("[%s ✗]", state.String())
					} else if state == StateComplete {
						return fmt.Sprintf("[%s ✓]", state.String())
					}
					return fmt.Sprintf("[%s]", state.String())
				}, decor.WC{W: 15}),
			),
			mpb.AppendDecorators(
				decor.Percentage(decor.WC{W: 5}),
			),
		)

		task.ProgressBar = bar
		tasks[i] = task
	}

	// Deploy services in parallel with controlled concurrency
	eg, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, m.maxParallel)

	resultsMu := sync.Mutex{}
	results := make(map[string]*project.ServiceDeployResult)

	for i, svc := range serviceConfigs {
		svc := svc       // capture loop variable
		task := tasks[i] // capture loop variable

		eg.Go(func() error {
			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }() // Release on exit
			case <-ctx.Done():
				return ctx.Err()
			}

			// Deploy the service with progress updates
			result, err := m.deployServiceWithProgress(ctx, svc, task)

			if err != nil {
				task.SetError(err)
				task.ProgressBar.Abort(false)
				return fmt.Errorf("deploying service %s: %w", svc.Name, err)
			}

			// Store result
			resultsMu.Lock()
			results[svc.Name] = result
			resultsMu.Unlock()

			task.UpdateState(StateComplete, "Complete")
			task.ProgressBar.SetCurrent(100)

			return nil
		})
	}

	// Wait for all deployments to complete
	deployErr := eg.Wait()

	// Wait for all progress bars to finish rendering
	p.Wait()

	return results, deployErr
}

// deployServiceWithProgress deploys a single service with progress updates
func (m *ParallelDeploymentManager) deployServiceWithProgress(
	ctx context.Context,
	svc *project.ServiceConfig,
	task *ServiceTask,
) (*project.ServiceDeployResult, error) {
	// Create service context for tracking artifacts
	serviceContext := project.NewServiceContext()

	// Use noop progress since we're tracking with MPB progress bars instead
	noopProgress := async.NewNoopProgress[project.ServiceProgress]()

	// Package phase (0-33%)
	task.UpdateState(StatePackaging, "Packaging")
	task.ProgressBar.SetCurrent(5)

	_, err := (*m.serviceManager).Package(ctx, svc, serviceContext, noopProgress, nil)
	if err != nil {
		return nil, fmt.Errorf("packaging: %w", err)
	}

	task.ProgressBar.SetCurrent(33)

	// Publish phase (33-66%)
	task.UpdateState(StatePublishing, "Publishing")

	_, err = (*m.serviceManager).Publish(ctx, svc, serviceContext, noopProgress, nil)
	if err != nil {
		return nil, fmt.Errorf("publishing: %w", err)
	}

	task.ProgressBar.SetCurrent(66)

	// Deploy phase (66-100%)
	task.UpdateState(StateDeploying, "Deploying")

	deployResult, err := (*m.serviceManager).Deploy(ctx, svc, serviceContext, noopProgress)
	if err != nil {
		return nil, fmt.Errorf("deploying: %w", err)
	}

	task.ProgressBar.SetCurrent(95)

	return deployResult, nil
}
