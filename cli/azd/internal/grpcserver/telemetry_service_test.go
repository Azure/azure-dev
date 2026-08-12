// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testExtensionId = "azd.internal.telemetry"

// stubExtensionLookup returns a configured error or installed record.
type stubExtensionLookup struct {
	extension     *extensions.Extension
	err           error
	sourceConfigs map[string]*extensions.SourceConfig
}

func (s stubExtensionLookup) GetInstalled(
	options extensions.FilterOptions,
) (*extensions.Extension, error) {
	if s.err != nil {
		return nil, s.err
	}

	if s.extension == nil || s.extension.Id != options.Id {
		return nil, extensions.ErrInstalledExtensionNotFound
	}

	return s.extension, nil
}

func (s stubExtensionLookup) IsOfficialRegistrySource(
	ctx context.Context,
	name string,
) (bool, error) {
	if s.sourceConfigs == nil {
		return strings.EqualFold(name, extensions.MainRegistryName), nil
	}

	source, ok := s.sourceConfigs[extensions.NormalizeSourceKey(name)]
	if !ok {
		return false, nil
	}

	return extensions.IsOfficialMainRegistrySource(source), nil
}

func testExtension() *extensions.Extension {
	return &extensions.Extension{
		Id:      testExtensionId,
		Version: "1.0.0",
		Source:  extensions.MainRegistryName,
	}
}

// callWith runs the handler as the given extension, mirroring how the auth
// interceptor injects host-signed claims.
func callWith(
	t *testing.T,
	extension *extensions.Extension,
	req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	t.Helper()

	return callWithContext(t, t.Context(), extension, req)
}

// callWithContext is callWith with a caller-supplied context, so a test can
// place the call inside an existing trace.
func callWithContext(
	t *testing.T,
	ctx context.Context,
	extension *extensions.Extension,
	req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	t.Helper()

	service := newTelemetryService(stubExtensionLookup{extension: extension})

	return callServiceWithContext(t, service, ctx, extension, req)
}

// callServiceWithContext runs the handler on an existing service, so a
// test can make several calls against one per-invocation budget.
func callServiceWithContext(
	t *testing.T,
	service *telemetryService,
	ctx context.Context,
	extension *extensions.Extension,
	req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	t.Helper()

	ctx = extensions.WithClaimsContext(ctx, &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: extension.Id},
		Capabilities:     extension.Capabilities,
	})

	return service.ReportUsage(ctx, req)
}

// usageSpansIn returns the recorded ext.usage spans belonging to traceId.
// The recorder is shared by the whole package, so filtering on the trace
// started by a single test keeps it independent of every other test.
func usageSpansIn(traceId trace.TraceID) []tracesdk.ReadOnlySpan {
	matched := []tracesdk.ReadOnlySpan{}

	for _, span := range testSpanRecorder.Ended() {
		if span.Name() != events.ExtensionUsageEvent {
			continue
		}

		if span.SpanContext().TraceID() == traceId {
			matched = append(matched, span)
		}
	}

	return matched
}

// attributesOf indexes a span's attributes by key. Spans also carry
// process-global attributes, so tests assert the ones this feature owns
// rather than matching the whole set.
func attributesOf(span tracesdk.ReadOnlySpan) map[attribute.Key]attribute.Value {
	indexed := map[attribute.Key]attribute.Value{}
	for _, attr := range span.Attributes() {
		indexed[attr.Key] = attr.Value
	}

	return indexed
}

func requireCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()

	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, expected, st.Code())
}

func TestNewTelemetryService(t *testing.T) {
	manager := &extensions.Manager{}

	service := NewTelemetryService(manager)

	telemetry, ok := service.(*telemetryService)
	require.True(t, ok)
	require.Same(t, manager, telemetry.extensions)
}

func Test_TelemetryService_RecordsEventWithAttributes(t *testing.T) {
	t.Parallel()

	// Start a command span first so the usage span can be checked to share
	// its trace, which is what makes the operation_Id join work downstream.
	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	extension := testExtension()
	resp, err := callWithContext(t, ctx, extension, &azdext.ReportUsageRequest{
		EventName: "deploy.completed",
		Attributes: map[string]string{
			"deploy.mode": "container",
			"retries":     "2",
		},
	})

	require.NoError(t, err)
	require.True(t, resp.Accepted)

	recorded := usageSpansIn(command.SpanContext().TraceID())
	require.Len(t, recorded, 1)

	attributes := attributesOf(recorded[0])
	for _, expected := range []attribute.KeyValue{
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(extension.Version),
		fields.ExtensionSource.String(extension.Source),
		fields.ExtensionEvent.String("deploy.completed"),
		attribute.String("ext.deploy.mode", "container"),
		attribute.String("ext.retries", "2"),
	} {
		require.Contains(t, attributes, expected.Key)
		require.Equal(t, expected.Value, attributes[expected.Key])
	}
}

func Test_TelemetryService_AcceptsEventWithoutAttributes(t *testing.T) {
	t.Parallel()

	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	resp, err := callWithContext(t, ctx, testExtension(), &azdext.ReportUsageRequest{
		EventName: "deploy.started",
	})

	require.NoError(t, err)
	require.True(t, resp.Accepted)

	recorded := usageSpansIn(command.SpanContext().TraceID())
	require.Len(t, recorded, 1)
	require.Equal(t,
		attribute.StringValue("deploy.started"),
		attributesOf(recorded[0])[fields.ExtensionEvent.Key])
}

func Test_TelemetryService_PrefixKeysSoTheyCannotOverwriteHostFields(t *testing.T) {
	t.Parallel()

	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	extension := testExtension()
	_, err := callWithContext(t, ctx, extension, &azdext.ReportUsageRequest{
		EventName: "deploy.completed",
		Attributes: map[string]string{
			string(fields.ExtensionId.Key):    "spoofed.extension",
			string(fields.ExtensionEvent.Key): "spoofed.event",
		},
	})
	require.NoError(t, err)

	recorded := usageSpansIn(command.SpanContext().TraceID())
	require.Len(t, recorded, 1)

	attributes := attributesOf(recorded[0])
	require.Equal(t, attribute.StringValue(extension.Id), attributes[fields.ExtensionId.Key])
	require.Equal(t,
		attribute.StringValue("deploy.completed"),
		attributes[fields.ExtensionEvent.Key])
	require.Equal(t,
		attribute.StringValue("spoofed.extension"),
		attributes[attribute.Key("ext.extension.id")])
}

func Test_TelemetryService_DropsEventFromUnofficialSource(t *testing.T) {
	t.Parallel()

	// Values are authored by the extension and are never reviewed at
	// runtime, so only registry-admitted extensions reach the pipeline.
	// A blank source is not official either: the upgrade resolver
	// defaults it to the main registry, but a gate cannot inherit that.
	sources := map[string]string{
		"dev registry": "dev",
		"blank":        "",
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
			defer command.End()

			extension := testExtension()
			extension.Source = source

			resp, err := callWithContext(t, ctx, extension, &azdext.ReportUsageRequest{
				EventName: "deploy.completed",
			})

			// Dropping is not an error, so an author runs the same
			// code path whether or not the event was kept.
			require.NoError(t, err)
			require.False(t, resp.Accepted)
			require.Empty(t, usageSpansIn(command.SpanContext().TraceID()))
		})
	}
}

func Test_TelemetryService_RejectsPollutedMainSource(t *testing.T) {
	t.Parallel()

	extension := testExtension()
	service := newTelemetryService(stubExtensionLookup{
		extension: extension,
		sourceConfigs: map[string]*extensions.SourceConfig{
			extensions.MainRegistryName: {
				Name:     extensions.MainRegistryName,
				Type:     extensions.SourceKindUrl,
				Location: "https://example.com/registry",
			},
		},
	})

	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	resp, err := callServiceWithContext(t, service, ctx, extension,
		&azdext.ReportUsageRequest{EventName: "deploy.completed"})

	require.NoError(t, err)
	require.False(t, resp.Accepted)
	require.Empty(t, usageSpansIn(command.SpanContext().TraceID()))
}

func Test_TelemetryService_ValidatesBeforeSourceGate(t *testing.T) {
	t.Parallel()

	extension := testExtension()
	extension.Source = "dev"
	service := newTelemetryService(stubExtensionLookup{extension: extension})

	_, err := callServiceWithContext(t, service, t.Context(), extension,
		&azdext.ReportUsageRequest{
			EventName: "deploy.completed",
			Attributes: map[string]string{
				"deploy.mode": strings.Repeat("v", maxUsageValueBytes+1),
			},
		})
	requireCode(t, err, codes.InvalidArgument)
	require.Zero(t, service.recorded.Load())

	resp, err := callServiceWithContext(t, service, t.Context(), extension,
		&azdext.ReportUsageRequest{EventName: "deploy.completed"})
	require.NoError(t, err)
	require.False(t, resp.Accepted)
}

func Test_TelemetryService_CapsEventsPerInvocation(t *testing.T) {
	t.Parallel()

	// The per-event bounds say nothing about how many events arrive,
	// and ReportUsage can be called in a loop.
	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	extension := testExtension()
	service := newTelemetryService(stubExtensionLookup{extension: extension})
	report := func() (*azdext.ReportUsageResponse, error) {
		return callServiceWithContext(t, service, ctx, extension,
			&azdext.ReportUsageRequest{EventName: "deploy.completed"})
	}

	for range maxUsageEventsPerInvocation {
		resp, err := report()
		require.NoError(t, err)
		require.True(t, resp.Accepted)
	}

	resp, err := report()
	require.NoError(t, err)
	require.False(t, resp.Accepted)
	require.Len(t,
		usageSpansIn(command.SpanContext().TraceID()),
		maxUsageEventsPerInvocation)
}

func Test_TelemetryService_RejectedCallDoesNotSpendBudget(t *testing.T) {
	t.Parallel()

	// Budget belongs to events that would otherwise be recorded, so a
	// buggy extension cannot starve a well-behaved one.
	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	extension := testExtension()
	service := newTelemetryService(stubExtensionLookup{extension: extension})

	for range maxUsageEventsPerInvocation {
		_, err := callServiceWithContext(t, service, ctx, extension,
			&azdext.ReportUsageRequest{
				EventName:  "deploy.completed",
				Attributes: map[string]string{"": "container"},
			})
		requireCode(t, err, codes.InvalidArgument)
	}

	resp, err := callServiceWithContext(t, service, ctx, extension,
		&azdext.ReportUsageRequest{EventName: "deploy.completed"})

	require.NoError(t, err)
	require.True(t, resp.Accepted)
	require.Len(t, usageSpansIn(command.SpanContext().TraceID()), 1)
}

func Test_TelemetryService_CapHoldsUnderConcurrentCalls(t *testing.T) {
	t.Parallel()

	// Service target providers deploy concurrently, so the budget has
	// to hold when several goroutines report at the same time.
	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	extension := testExtension()
	service := newTelemetryService(stubExtensionLookup{extension: extension})

	var wg sync.WaitGroup
	var accepted, failed atomic.Int64

	for range maxUsageEventsPerInvocation * 2 {
		wg.Go(func() {
			resp, err := callServiceWithContext(t, service, ctx, extension,
				&azdext.ReportUsageRequest{EventName: "deploy.completed"})
			if err != nil {
				failed.Add(1)
				return
			}

			if resp.Accepted {
				accepted.Add(1)
			}
		})
	}

	wg.Wait()

	require.Zero(t, failed.Load())
	require.Equal(t, int64(maxUsageEventsPerInvocation), accepted.Load())
	require.Len(t,
		usageSpansIn(command.SpanContext().TraceID()),
		maxUsageEventsPerInvocation)
}

func Test_TelemetryService_RecordsNoSpanWhenRejected(t *testing.T) {
	t.Parallel()

	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	_, err := callWithContext(t, ctx, testExtension(), &azdext.ReportUsageRequest{
		EventName:  "deploy.completed",
		Attributes: map[string]string{"": "container"},
	})

	requireCode(t, err, codes.InvalidArgument)
	require.Empty(t, usageSpansIn(command.SpanContext().TraceID()))
}

func Test_TelemetryService_RequiresClaims(t *testing.T) {
	t.Parallel()

	service := newTelemetryService(stubExtensionLookup{extension: testExtension()})
	_, err := service.ReportUsage(t.Context(), &azdext.ReportUsageRequest{
		EventName: "deploy.completed",
	})

	requireCode(t, err, codes.Unauthenticated)
}

func Test_TelemetryService_RejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	tooManyAttributes := map[string]string{}
	for i := range maxUsageAttributes + 1 {
		tooManyAttributes[fmt.Sprintf("key%d", i)] = "value"
	}

	tests := map[string]*azdext.ReportUsageRequest{
		"nil":               nil,
		"missing eventName": {},
		"long eventName": {
			EventName: strings.Repeat("e", maxUsageEventNameBytes+1),
		},
		"empty key": {
			EventName:  "deploy.completed",
			Attributes: map[string]string{"": "container"},
		},
		"long key": {
			EventName: "deploy.completed",
			Attributes: map[string]string{
				strings.Repeat("k", maxUsageKeyBytes+1): "container",
			},
		},
		"long value": {
			EventName: "deploy.completed",
			Attributes: map[string]string{
				"deploy.mode": strings.Repeat("v", maxUsageValueBytes+1),
			},
		},
		"too many attributes": {
			EventName:  "deploy.completed",
			Attributes: tooManyAttributes,
		},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := callWith(t, testExtension(), req)
			requireCode(t, err, codes.InvalidArgument)
		})
	}
}

func Test_TelemetryService_AcceptsValuesAtTheBounds(t *testing.T) {
	t.Parallel()

	ctx, command := testTracerProvider.Tracer("test").Start(t.Context(), "cmd.deploy")
	defer command.End()

	key := strings.Repeat("k", maxUsageKeyBytes)
	resp, err := callWithContext(t, ctx, testExtension(), &azdext.ReportUsageRequest{
		EventName: strings.Repeat("e", maxUsageEventNameBytes),
		Attributes: map[string]string{
			key: strings.Repeat("v", maxUsageValueBytes),
		},
	})

	require.NoError(t, err)
	require.True(t, resp.Accepted)

	recorded := usageSpansIn(command.SpanContext().TraceID())
	require.Len(t, recorded, 1)
	require.Contains(t, attributesOf(recorded[0]), attribute.Key("ext."+key))
}

func Test_TelemetryService_UsesUtf8ByteLimits(t *testing.T) {
	t.Parallel()

	accepted := strings.Repeat("é", 63)
	rejected := accepted + "éa"

	resp, err := callWith(t, testExtension(), &azdext.ReportUsageRequest{
		EventName: accepted,
	})
	require.NoError(t, err)
	require.True(t, resp.Accepted)

	_, err = callWith(t, testExtension(), &azdext.ReportUsageRequest{
		EventName: rejected,
	})
	requireCode(t, err, codes.InvalidArgument)
}

func Test_TelemetryService_RejectsUninstalledExtension(t *testing.T) {
	t.Parallel()

	service := newTelemetryService(stubExtensionLookup{})
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: testExtensionId},
	})

	_, err := service.ReportUsage(ctx, &azdext.ReportUsageRequest{
		EventName: "deploy.completed",
	})

	requireCode(t, err, codes.PermissionDenied)
}

func Test_TelemetryService_ReportsLookupFailure(t *testing.T) {
	t.Parallel()

	service := newTelemetryService(stubExtensionLookup{
		err: errors.New("failed to read installed config"),
	})
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: testExtensionId},
	})

	_, err := service.ReportUsage(ctx, &azdext.ReportUsageRequest{
		EventName: "deploy.completed",
	})

	requireCode(t, err, codes.Internal)
	require.NotContains(t, err.Error(), "installed config")
}

func Test_TelemetryService_DoesNotEchoCallerText(t *testing.T) {
	t.Parallel()

	_, err := callWith(t, testExtension(), &azdext.ReportUsageRequest{
		EventName:  "deploy.completed",
		Attributes: map[string]string{strings.Repeat("secret", 40): "container"},
	})

	requireCode(t, err, codes.InvalidArgument)
	require.NotContains(t, err.Error(), "secret")
}

func Test_TelemetryService_IdentifiesOversizedValueKey(t *testing.T) {
	t.Parallel()

	key := "deploy.mode"
	value := strings.Repeat("v", maxUsageValueBytes+1)
	_, err := callWith(t, testExtension(), &azdext.ReportUsageRequest{
		EventName:  "deploy.completed",
		Attributes: map[string]string{key: value},
	})

	requireCode(t, err, codes.InvalidArgument)
	require.Contains(t, err.Error(), key)
	require.NotContains(t, err.Error(), value)
}
