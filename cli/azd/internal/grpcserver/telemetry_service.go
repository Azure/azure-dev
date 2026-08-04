// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Bounds on reported usage. These are shape and volume limits, not
// content review: the host does not judge what an attribute means, only
// that it cannot corrupt the shared telemetry pipeline.
const (
	// maxUsageAttributes caps attributes per event. The exporter enforces
	// no such limit, so this guards against a loop inflating span size.
	maxUsageAttributes = 32
	// maxUsageEventNameLength bounds the extension.event value, which is a
	// query dimension and should stay low cardinality.
	maxUsageEventNameLength = 128
	// maxUsageKeyLength bounds a caller key. The App Insights contracts
	// library truncates and renames property names longer than 150
	// characters, which would silently merge two distinct keys into one
	// column. Prefixed with "ext.", 128 stays well clear of that.
	maxUsageKeyLength = 128
	// maxUsageValueLength bounds a caller value. The exporter truncates at
	// 8192 with only a diagnostic, so this is a cardinality and cost
	// policy: a payload dump fails loudly here instead of quietly landing
	// in the pipeline.
	maxUsageValueLength = 512
	// maxUsageEventsPerInvocation caps recorded events for one azd
	// process. Every other bound is per event and says nothing about
	// how many arrive, and ReportUsage can be called in a loop.
	maxUsageEventsPerInvocation = 100
)

// installedExtensionLookup resolves the installed extension record for a
// signed extension id. *extensions.Manager satisfies it.
type installedExtensionLookup interface {
	GetInstalled(options extensions.FilterOptions) (*extensions.Extension, error)
}

// telemetryService implements azdext.TelemetryServiceServer.
type telemetryService struct {
	azdext.UnimplementedTelemetryServiceServer
	extensions installedExtensionLookup
	// recorded counts the events kept for this azd process. Service
	// target providers deploy concurrently, so it has to be atomic.
	recorded atomic.Int64
}

// NewTelemetryService creates the telemetry gRPC service. The extension
// manager resolves the calling extension's installed record so the host can
// stamp identity the caller never supplies. Returning the interface type lets
// the IoC container satisfy the azdext.TelemetryServiceServer parameter on
// NewServer without an adapter.
func NewTelemetryService(manager *extensions.Manager) azdext.TelemetryServiceServer {
	return newTelemetryService(manager)
}

func newTelemetryService(lookup installedExtensionLookup) *telemetryService {
	return &telemetryService{extensions: lookup}
}

// ReportUsage records one usage event for the calling extension. The
// extension supplies only an event name and attribute map; the host
// stamps identity from the signed claims and the installed record, so
// a caller cannot assert which extension it is.
//
// Two outcomes are deliberately not errors. An extension outside the
// official registry, and one past the per-invocation event budget, get
// Accepted: false and no span. Reporting is best effort, so an author
// runs the same code path whether or not the event was kept.
//
// A malformed request is an error, and fails closed on the whole
// request: exceeding any bound records nothing, because dropping the
// offending attribute alone would ship data that looks complete but is
// not. Rejected caller text is never echoed back.
func (s *telemetryService) ReportUsage(
	ctx context.Context,
	req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "validated extension claims are required")
	}

	if req == nil || req.EventName == "" || len(req.EventName) > maxUsageEventNameLength {
		return nil, status.Errorf(codes.InvalidArgument,
			"event name is required and must be at most %d characters", maxUsageEventNameLength)
	}

	if len(req.Attributes) > maxUsageAttributes {
		return nil, status.Errorf(codes.InvalidArgument,
			"event declares more than %d attributes", maxUsageAttributes)
	}

	extension, err := s.extensions.GetInstalled(extensions.FilterOptions{Id: claims.Subject})
	if err != nil {
		if errors.Is(err, extensions.ErrInstalledExtensionNotFound) {
			return nil, status.Error(codes.PermissionDenied, "extension is not installed")
		}

		return nil, status.Error(codes.Internal, "failed to verify installed extension")
	}

	// Only extensions admitted to the official azd registry report
	// usage. Values are authored by the extension and are never
	// reviewed at runtime, so registry admission is what keeps
	// unchecked content out of azd's shared pipeline.
	//
	// This is a source check, not proof of first party. Registry
	// admission is the durable control and this only reflects it, so a
	// future third-party entry in the official registry would report
	// too. A blank source is not treated as official: the upgrade
	// resolver defaults it to the main registry, but a gate cannot
	// inherit that leniency.
	if !strings.EqualFold(extension.Source, extensions.MainRegistryName) {
		log.Printf(
			"telemetry: dropping usage event from %s installed from source %q",
			extension.Id, extension.Source)

		return &azdext.ReportUsageResponse{Accepted: false}, nil
	}

	attributes := []attribute.KeyValue{
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(extension.Version),
		fields.ExtensionSource.String(extension.Source),
		fields.ExtensionEvent.String(req.EventName),
	}

	for key, value := range req.Attributes {
		if key == "" || len(key) > maxUsageKeyLength {
			return nil, status.Errorf(codes.InvalidArgument,
				"attribute keys are required and must be at most %d characters", maxUsageKeyLength)
		}

		if len(value) > maxUsageValueLength {
			return nil, status.Errorf(codes.InvalidArgument,
				"attribute values must be at most %d characters", maxUsageValueLength)
		}

		attributes = append(attributes, fields.ExtensionUsageAttribute(key).String(value))
	}

	// Budget is spent only by events that would otherwise be recorded,
	// so a malformed call cannot starve a well-behaved one. The count
	// covers the whole azd process because the service is registered as
	// a container singleton, which makes it a per-invocation budget
	// even when a composite command such as up runs several actions.
	if s.recorded.Add(1) > maxUsageEventsPerInvocation {
		log.Printf(
			"telemetry: dropping usage event from %s, limit of %d reached",
			extension.Id, maxUsageEventsPerInvocation)

		return &azdext.ReportUsageResponse{Accepted: false}, nil
	}

	// Record a dedicated span rather than augmenting the command span. The
	// extension's trace context arrives over gRPC metadata, so this span
	// shares the command's trace and joins on operation_Id downstream.
	_, span := tracing.Start(ctx, events.ExtensionUsageEvent)
	span.SetAttributes(attributes...)
	span.End()

	return &azdext.ReportUsageResponse{Accepted: true}, nil
}
