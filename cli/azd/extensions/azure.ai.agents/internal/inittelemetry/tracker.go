// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package inittelemetry records elapsed time for Init business steps.
package inittelemetry

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

// Step is a fixed identifier, never a question, answer, path, or resource name.
type Step string

const (
	InputValidation          Step = "input_validation"
	AiServiceCheck           Step = "ai_service_check"
	Authentication           Step = "authentication"
	InitModeSelection        Step = "init_mode_selection"
	TemplateSelection        Step = "template_selection"
	TemplateLoading          Step = "template_loading"
	TemplateStaging          Step = "template_staging"
	ProjectScaffolding       Step = "project_scaffolding"
	AgentNameSelection       Step = "agent_name_selection"
	AgentConfiguration       Step = "agent_configuration"
	DeployModeSelection      Step = "deploy_mode_selection"
	CodeConfiguration        Step = "code_configuration"
	ProtocolSelection        Step = "protocol_selection"
	SubscriptionSelection    Step = "subscription_selection"
	LocationSelection        Step = "location_selection"
	FoundryProjectSelection  Step = "foundry_project_selection"
	ModelSelection           Step = "model_selection"
	ModelDeploymentSelection Step = "model_deployment_selection"
	PromptAgentConfiguration Step = "prompt_agent_configuration"
	RaiPolicyConfiguration   Step = "rai_policy_configuration"
	EnvironmentConfiguration Step = "environment_configuration"
	ProjectConfiguration     Step = "project_configuration"
	ProjectAuthoring         Step = "project_authoring"
	ModelDeploymentAuthoring Step = "model_deployment_authoring"
	FileGeneration           Step = "file_generation"
	InfraGeneration          Step = "infra_generation"
)

const maxSteps = 96

type contextKey struct{}

type step struct {
	id       Step
	duration time.Duration
	complete bool
}

// Tracker buffers timings so recording a step never waits for telemetry I/O.
type Tracker struct {
	mu      sync.Mutex
	now     func() time.Time
	steps   []step
	limited bool
}

// NewContext enables timing only for this Init invocation.
func NewContext(ctx context.Context) (context.Context, *Tracker) {
	tracker := &Tracker{now: time.Now}
	return context.WithValue(ctx, contextKey{}, tracker), tracker
}

// Start returns an idempotent completion function. Use defer Start(ctx, id)()
// to include all work in a step, including loading, user input, and validation.
func Start(ctx context.Context, id Step) func() {
	tracker, ok := ctx.Value(contextKey{}).(*Tracker)
	if !ok {
		return func() {}
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.steps) == maxSteps {
		if !tracker.limited {
			azdext.NewLogger("init.telemetry").Debug("Init timing buffer limit reached")
			tracker.limited = true
		}
		return func() {}
	}
	index := len(tracker.steps)
	started := tracker.now()
	tracker.steps = append(tracker.steps, step{id: id})
	return func() {
		tracker.mu.Lock()
		defer tracker.mu.Unlock()
		if !tracker.steps[index].complete {
			tracker.steps[index].duration = tracker.now().Sub(started)
			tracker.steps[index].complete = true
		}
	}
}

// Report uses the existing host telemetry API and never changes Init's result.
func (t *Tracker) Report(ctx context.Context) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		azdext.NewLogger("init.telemetry").Debug("could not create Init telemetry client")
		return
	}
	defer client.Close()
	t.Flush(ctx, foundryTelemetry.NewReporter(client.Telemetry(), nil))
}

// Report flushes the invocation's timings after existing successful post-run
// telemetry. Commands without an Init tracker do not report timing events.
func Report(ctx context.Context) {
	if tracker, ok := ctx.Value(contextKey{}).(*Tracker); ok {
		tracker.Report(ctx)
	}
}

// Flush preserves trace metadata on cancellation and bounds the entire batch
// by one second, rather than adding a reporting timeout for every step.
func (t *Tracker) Flush(ctx context.Context, reporter foundryTelemetry.Reporter) {
	t.mu.Lock()
	steps := append([]step(nil), t.steps...)
	t.mu.Unlock()
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	for i, step := range steps {
		if reportCtx.Err() != nil {
			azdext.NewLogger("init.telemetry").Debug("Init timing reporting deadline reached")
			break
		}
		if step.complete {
			reporter.Report(reportCtx, foundryTelemetry.Event{
				Name: "agent.init.step",
				Attributes: map[string]string{
					"agent.init.step.id":          string(step.id),
					"agent.init.step.sequence":    strconv.Itoa(i + 1),
					"agent.init.step.duration_ms": strconv.FormatFloat(step.duration.Seconds()*1000, 'f', 3, 64),
				},
			})
		}
	}
}
