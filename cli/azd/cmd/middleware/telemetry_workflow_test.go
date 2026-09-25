// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTelemetryMiddleware_WorkflowStepsExcludeExtensionDrops(t *testing.T) {
	// Not parallel: installs the process-wide tracer provider and mutates the
	// process-global usage attributes.
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder)))
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)

	const retainedKey = attribute.Key("test.retained")
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})
	mockContext := mocks.NewMockContext(t.Context())

	runStep := func(ctx context.Context, name string, drop bool) {
		step := NewTelemetryMiddleware(
			&Options{CommandPath: "azd " + name, Name: name}, lazyPlatformConfig, nil)
		_, err := step.Run(WithChildAction(ctx), func(context.Context) (*actions.ActionResult, error) {
			if drop {
				tracing.AppendUsageAttributeUnique(
					fields.ExtensionUsageDropped.String("publisher.extension@budget_exhausted"))
				tracing.IncrementUsageAttribute(fields.ExtensionUsageDroppedCount.Int64(1))
			}
			return nil, nil
		})
		require.NoError(t, err)
	}

	root := NewTelemetryMiddleware(&Options{CommandPath: "azd up", Name: "up"}, lazyPlatformConfig, nil)
	_, err := root.Run(*mockContext.Context, func(ctx context.Context) (*actions.ActionResult, error) {
		tracing.SetUsageAttributes(retainedKey.String("value"))
		runStep(ctx, "provision", true)
		runStep(ctx, "deploy", false)
		return nil, nil
	})
	require.NoError(t, err)

	spans := map[string]map[attribute.Key]attribute.Value{}
	for _, span := range recorder.Ended() {
		attrs := map[attribute.Key]attribute.Value{}
		for _, attr := range span.Attributes() {
			attrs[attr.Key] = attr.Value
		}
		spans[span.Name()] = attrs
	}

	for _, name := range []string{"cmd.provision", "cmd.deploy"} {
		require.Contains(t, spans, name)
		require.Contains(t, spans[name], retainedKey, name)
		require.NotContains(t, spans[name], fields.ExtensionUsageDropped.Key, name)
		require.NotContains(t, spans[name], fields.ExtensionUsageDroppedCount.Key, name)
	}

	require.Contains(t, spans, "cmd.up")
	require.Equal(t,
		[]string{"publisher.extension@budget_exhausted"},
		spans["cmd.up"][fields.ExtensionUsageDropped.Key].AsStringSlice())
	require.Equal(t, int64(1), spans["cmd.up"][fields.ExtensionUsageDroppedCount.Key].AsInt64())
}
