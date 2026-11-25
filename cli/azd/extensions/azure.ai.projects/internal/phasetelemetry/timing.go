// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package phasetelemetry records extension-owned operation and step timings.
package phasetelemetry

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Name identifies an operation, never a command's user-supplied arguments.
type Name string

const (
	ProjectAuthoring         Name = "project_authoring"
	ModelDeploymentAuthoring Name = "model_deployment_authoring"
	Provision                Name = "provision"
	Package                  Name = "package"
	Publish                  Name = "publish"
	Deploy                   Name = "deploy"
)

// Scope distinguishes a project command from an extension provider method.
type Scope string

const (
	Command  Scope = "command"
	Provider Scope = "provider"
)

// Step identifies a fixed business operation within a phase.
type Step string

const (
	ProjectResolution        Step = "project_resolution"
	SubscriptionSelection    Step = "subscription_selection"
	LocationSelection        Step = "location_selection"
	AzureQuery               Step = "azure_query"
	DeploymentReconciliation Step = "deployment_reconciliation"
	ConfigurationSave        Step = "configuration_save"
	ConfigurationLoading     Step = "configuration_loading"
	Validation               Step = "validation"
	//nolint:gosec // G101: fixed telemetry identifier, not a credential.
	CredentialAcquisition       Step = "credential_acquisition"
	TemplateResolution          Step = "template_resolution"
	TemplateCompilation         Step = "template_compilation"
	ResourceLookup              Step = "resource_lookup"
	ResourceDeploymentSubmit    Step = "resource_deployment_submit"
	ResourceDeploymentWait      Step = "resource_deployment_wait"
	CodePackaging               Step = "code_packaging"
	ImageBuild                  Step = "image_build"
	ImagePackaging              Step = "image_packaging"
	ImagePush                   Step = "image_push"
	ArtifactUpload              Step = "artifact_upload"
	DeployContextResolution     Step = "deploy_context_resolution"
	DependencyValidation        Step = "dependency_validation"
	AgentCreateOrUpdate         Step = "agent_create_or_update"
	AgentActivationWait         Step = "agent_activation_wait"
	EndpointUpdate              Step = "endpoint_update"
	VoiceTelephonyConfiguration Step = "voice_telephony_configuration"
)

const maxSteps = 24

type contextKey struct{}

type step struct {
	id       Step
	started  time.Time
	duration time.Duration
	complete bool
}

// Timing owns one invocation-local attempt; provider instances never store it.
type Timing struct {
	mu       sync.Mutex
	now      func() time.Time
	started  time.Time
	name     Name
	scope    Scope
	attempt  string
	reporter foundryTelemetry.Reporter
	steps    []step
	dropped  int
	finished bool
	timedOut bool
}

// Timeout marks an observed business polling deadline without changing its error.
func Timeout(ctx context.Context) {
	if timing, ok := ctx.Value(contextKey{}).(*Timing); ok {
		timing.mu.Lock()
		timing.timedOut = true
		timing.mu.Unlock()
	}
}

// Reporter reuses the existing host client; a provider without a host emits nothing.
func Reporter(client *azdext.AzdClient) foundryTelemetry.Reporter {
	if client == nil {
		return nil
	}
	return foundryTelemetry.NewReporter(client.Telemetry(), nil)
}

// HostReporter creates a telemetry-only client for a command whose action owns
// and closes its business client. Failure never changes the business result.
func HostReporter() (foundryTelemetry.Reporter, func()) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		azdext.NewLogger("phase.telemetry").Debug("could not create phase telemetry client")
		return nil, func() {}
	}
	return Reporter(client), client.Close
}

// Begin starts one observable command or provider operation, preserving the trace.
func Begin(ctx context.Context, name Name, scope Scope, reporter foundryTelemetry.Reporter) (context.Context, *Timing) {
	timing := &Timing{
		now: time.Now, started: time.Now(), name: name, scope: scope,
		attempt: rand.Text(), reporter: reporter,
	}
	return context.WithValue(ctx, contextKey{}, timing), timing
}

// Start returns an idempotent completion function. A step includes all internal
// loading, interaction, validation, and retries; it does not perform reporting I/O.
func Start(ctx context.Context, id Step) func() {
	timing, ok := ctx.Value(contextKey{}).(*Timing)
	if !ok {
		return func() {}
	}
	timing.mu.Lock()
	defer timing.mu.Unlock()
	if len(timing.steps) == maxSteps {
		timing.dropped++
		return func() {}
	}
	index := len(timing.steps)
	timing.steps = append(timing.steps, step{id: id, started: timing.now()})
	return func() {
		timing.mu.Lock()
		defer timing.mu.Unlock()
		if !timing.steps[index].complete {
			timing.steps[index].duration = timing.now().Sub(timing.steps[index].started)
			timing.steps[index].complete = true
		}
	}
}

// End records the actual return boundary before sending the completion first
// and steps afterward, with one shared one-second reporting deadline.
func (t *Timing) End(ctx context.Context, err error) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.finished = true
	ended := t.now()
	steps := append([]step(nil), t.steps...)
	dropped := t.dropped
	timedOut := t.timedOut
	t.mu.Unlock()
	if t.reporter == nil {
		return
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	result := outcome(err)
	if result == "error" && timedOut {
		result = "timeout"
	}
	t.reporter.Report(reportCtx, foundryTelemetry.Event{
		Name: "agent.phase.completed",
		Attributes: map[string]string{
			"agent.phase.name":               string(t.name),
			"agent.phase.scope":              string(t.scope),
			"agent.phase.attempt_id":         t.attempt,
			"agent.phase.start_time":         t.started.UTC().Format(time.RFC3339Nano),
			"agent.phase.end_time":           ended.UTC().Format(time.RFC3339Nano),
			"agent.phase.duration_ms":        milliseconds(ended.Sub(t.started)),
			"agent.phase.outcome":            result,
			"agent.phase.step.dropped_count": strconv.Itoa(dropped),
		},
	})
	for i, step := range steps {
		if reportCtx.Err() != nil {
			azdext.NewLogger("phase.telemetry").Debug("phase timing reporting deadline reached")
			break
		}
		if step.complete {
			t.reporter.Report(reportCtx, foundryTelemetry.Event{
				Name: "agent.phase.step",
				Attributes: map[string]string{
					"agent.phase.name":                 string(t.name),
					"agent.phase.scope":                string(t.scope),
					"agent.phase.attempt_id":           t.attempt,
					"agent.phase.step.id":              string(step.id),
					"agent.phase.step.sequence":        strconv.Itoa(i + 1),
					"agent.phase.step.start_offset_ms": milliseconds(step.started.Sub(t.started)),
					"agent.phase.step.duration_ms":     milliseconds(step.duration),
				},
			})
		}
	}
}

func milliseconds(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds()*1000, 'f', 3, 64)
}

func outcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return "timeout"
	}
	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return "canceled"
	}
	if local, ok := errors.AsType[*azdext.LocalError](err); ok &&
		local.Category == azdext.LocalErrorCategoryUser && local.Code == "cancelled" {
		return "canceled"
	}
	return "error"
}
