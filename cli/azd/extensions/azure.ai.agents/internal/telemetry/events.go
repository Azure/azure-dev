// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	agentInvokeSelectedEvent          = "agent.invoke.selected"
	agentSnapshotDeployCompletedEvent = "agent.snapshot.deploy.completed"
	invokeProtocolAttribute           = "agent.invoke.protocol"
	invokeLongRunningAttribute        = "agent.invoke.long_running"
	invokeNoWaitAttribute             = "agent.invoke.no_wait"
	localClientRouteSelectedEvent     = "local_client.route.selected"
	localClientRouteAttribute         = "route"
)

type queuedDeploymentEvent struct {
	ctx   context.Context
	event foundryTelemetry.Event
}

// DeploymentReporter sends bounded best-effort events outside service return paths.
type DeploymentReporter struct {
	mu     sync.Mutex
	closed bool
	queue  chan queuedDeploymentEvent
	done   chan struct{}
	cancel context.CancelFunc
}

// NewDeploymentReporter starts a single worker, owned by the listen command.
func NewDeploymentReporter(reporter foundryTelemetry.Reporter) *DeploymentReporter {
	ctx, cancel := context.WithCancel(context.Background())
	result := &DeploymentReporter{
		queue: make(chan queuedDeploymentEvent, 100), done: make(chan struct{}), cancel: cancel,
	}
	go func() {
		defer close(result.done)
		for pending := range result.queue {
			if ctx.Err() != nil {
				return
			}
			reportCtx, cancelReport := context.WithTimeout(context.WithoutCancel(pending.ctx), time.Second)
			stop := context.AfterFunc(ctx, cancelReport)
			reporter.Report(reportCtx, pending.event)
			stop()
			cancelReport()
		}
	}()
	return result
}

// Report enqueues without waiting for a telemetry RPC; full or closed queues drop events.
func (reporter *DeploymentReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if reporter.closed {
		return
	}
	select {
	case reporter.queue <- queuedDeploymentEvent{ctx: ctx, event: event}:
	default:
	}
}

// Close drains for at most one second, then cancels outstanding reporting before returning.
func (reporter *DeploymentReporter) Close() {
	reporter.mu.Lock()
	if !reporter.closed {
		reporter.closed = true
		close(reporter.queue)
	}
	reporter.mu.Unlock()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-reporter.done:
	case <-timer.C:
		reporter.cancel()
		<-reporter.done
	}
	reporter.cancel()
}

// AgentInvokeSelected creates the adoption event for a resolved remote hosted-agent invoke.
// It records the selected request mode, not a service response or success.
// The caller supplies the protocol resolved and validated by the invoke command.
func AgentInvokeSelected(protocol string, longRunning, noWait bool) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: agentInvokeSelectedEvent,
		Attributes: map[string]string{
			invokeProtocolAttribute:    protocol,
			invokeLongRunningAttribute: strconv.FormatBool(longRunning),
			invokeNoWaitAttribute:      strconv.FormatBool(noWait),
		},
	}
}

// AgentSnapshotDeployCompleted reports the client-observed result, not a snapshot lifecycle outcome.
func AgentSnapshotDeployCompleted(ctx context.Context, err error, elapsed time.Duration) foundryTelemetry.Event {
	if err != nil && ctx.Err() != nil {
		if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			err = context.DeadlineExceeded
		} else {
			err = ctx.Err()
		}
	}
	outcome, code := "succeeded", "none"
	if err != nil {
		outcome, code = "failed", "other"
		localErr, _ := errors.AsType[*azdext.LocalError](err)
		switch {
		case errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded:
			outcome, code = "timeout", "deadline_exceeded"
		case exterrors.IsCancellation(err) || localErr != nil && localErr.Code == exterrors.CodeCancelled:
			outcome, code = "cancelled", "cancelled"
		default:
			if serviceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
				switch serviceErr.ErrorCode {
				case "create_agent.timeout":
					outcome, code = "timeout", serviceErr.ErrorCode
				case "create_agent.SnapshotCreationError", "create_agent.ProvisioningError",
					"create_agent.ImageError", "create_agent.CodeError":
					code = serviceErr.ErrorCode
				}
			}
		}
	}
	return foundryTelemetry.Event{
		Name: agentSnapshotDeployCompletedEvent,
		Attributes: map[string]string{
			"agent.memory_snapshot":    "true",
			"agent.deploy.outcome":     outcome,
			"agent.deploy.error_code":  code,
			"agent.deploy.duration_ms": strconv.FormatInt(max(elapsed.Milliseconds(), 0), 10),
		},
	}
}

// LocalClientRoute identifies the client selected for a local agent run.
type LocalClientRoute string

const (
	LocalClientRouteInspector  LocalClientRoute = "inspector"
	LocalClientRoutePlayground LocalClientRoute = "playground"
	LocalClientRouteSuppressed LocalClientRoute = "suppressed"
)

// LocalClientRouteSelected creates the event emitted after resolving the local client route.
func LocalClientRouteSelected(route LocalClientRoute) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: localClientRouteSelectedEvent,
		Attributes: map[string]string{
			localClientRouteAttribute: string(route),
		},
	}
}
