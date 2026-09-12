// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"log"
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
	// maxUsageEventNameBytes bounds the extension.event value.
	// The value is a query dimension and should stay low cardinality.
	maxUsageEventNameBytes = 128
	// maxUsageKeyBytes bounds a caller key. The App Insights contracts
	// library truncates and renames property names longer than 150 UTF-8
	// bytes, which would silently merge distinct keys into one column.
	maxUsageKeyBytes = 128
	// maxUsageValueBytes bounds a caller value. The exporter
	// truncates at 8192 bytes with only a diagnostic, so this is a
	// cardinality and cost policy: payload dumps fail loudly here.
	maxUsageValueBytes = 512
	// maxUsageEventsPerInvocation caps recorded events for one azd
	// process. Every other bound is per event and says nothing about
	// how many arrive, and ReportUsage can be called in a loop.
	maxUsageEventsPerInvocation = 100
)

type extensionUsageDropReason string

const (
	extensionUsageDropReasonEventNameInvalid       extensionUsageDropReason = "event_name_invalid"
	extensionUsageDropReasonAttributeCountExceeded extensionUsageDropReason = "attribute_count_exceeded"
	extensionUsageDropReasonAttributeKeyInvalid    extensionUsageDropReason = "attribute_key_invalid"
	extensionUsageDropReasonAttributeValueTooLong  extensionUsageDropReason = "attribute_value_too_long"
	extensionUsageDropReasonNotInstalled           extensionUsageDropReason = "not_installed"
	extensionUsageDropReasonLookupFailed           extensionUsageDropReason = "lookup_failed"
	extensionUsageDropReasonSourceCheckFailed      extensionUsageDropReason = "source_check_failed"
	extensionUsageDropReasonSourceIneligible       extensionUsageDropReason = "source_ineligible"
	extensionUsageDropReasonBudgetExhausted        extensionUsageDropReason = "budget_exhausted"
	extensionUsageDropReasonUnauthenticated        extensionUsageDropReason = "unauthenticated"

	unattributedExtensionId = "unattributed"
)

// installedExtensionLookup resolves the installed extension record for a
// signed extension id. *extensions.Manager satisfies it.
type installedExtensionLookup interface {
	GetInstalled(options extensions.FilterOptions) (*extensions.Extension, error)
	IsOfficialRegistrySource(ctx context.Context, name string) (bool, error)
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
		recordExtensionUsageDrop(unattributedExtensionId, extensionUsageDropReasonUnauthenticated)
		return nil, status.Error(codes.Unauthenticated, "validated extension claims are required")
	}

	extension, err := s.extensions.GetInstalled(extensions.FilterOptions{Id: claims.Subject})
	if err != nil {
		if errors.Is(err, extensions.ErrInstalledExtensionNotFound) {
			recordExtensionUsageDrop(unattributedExtensionId, extensionUsageDropReasonNotInstalled)
			return nil, status.Error(codes.PermissionDenied, "extension is not installed")
		}

		recordExtensionUsageDrop(unattributedExtensionId, extensionUsageDropReasonLookupFailed)
		return nil, status.Error(codes.Internal, "failed to verify installed extension")
	}

	official, err := s.extensions.IsOfficialRegistrySource(ctx, extension.Source)
	if err != nil {
		recordExtensionUsageDrop(unattributedExtensionId, extensionUsageDropReasonSourceCheckFailed)
		log.Printf(
			"telemetry: failed to verify source %q for %s: %v",
			extension.Source, extension.Id, err)
		return &azdext.ReportUsageResponse{Accepted: false}, nil
	}

	if !official {
		recordExtensionUsageDrop(unattributedExtensionId, extensionUsageDropReasonSourceIneligible)
		log.Printf(
			"telemetry: dropping usage event from %s installed from source %q",
			extension.Id, extension.Source)

		return &azdext.ReportUsageResponse{Accepted: false}, nil
	}

	if reason, err := validateUsageRequest(req); err != nil {
		recordExtensionUsageDrop(extension.Id, reason)
		return nil, err
	}

	attributes := []attribute.KeyValue{
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(extension.Version),
		fields.ExtensionSource.String(extension.Source),
		fields.ExtensionEvent.String(req.EventName),
	}

	for key, value := range req.Attributes {
		attributes = append(attributes, fields.ExtensionUsageAttribute(key).String(value))
	}

	// Budget is spent only by events that would otherwise be recorded,
	// so a malformed call cannot starve a well-behaved one. The count
	// covers the whole azd process because the service is registered as
	// a container singleton, which makes it a per-invocation budget
	// even when a composite command such as up runs several actions.
	if s.recorded.Add(1) > maxUsageEventsPerInvocation {
		recordExtensionUsageDrop(extension.Id, extensionUsageDropReasonBudgetExhausted)
		log.Printf(
			"telemetry: dropping usage event %q from %s, limit of %d reached",
			req.EventName, extension.Id, maxUsageEventsPerInvocation)

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

func recordExtensionUsageDrop(extensionId string, reason extensionUsageDropReason) {
	tracing.AppendUsageAttributeUnique(
		fields.ExtensionUsageDropped.String(extensionId + "@" + string(reason)),
	)
	tracing.IncrementUsageAttribute(fields.ExtensionUsageDroppedCount.Int64(1))
}

func validateUsageRequest(req *azdext.ReportUsageRequest) (extensionUsageDropReason, error) {
	if req == nil || req.EventName == "" || len(req.EventName) > maxUsageEventNameBytes {
		return extensionUsageDropReasonEventNameInvalid, status.Errorf(codes.InvalidArgument,
			"event name is required and must be at most %d UTF-8 bytes",
			maxUsageEventNameBytes)
	}

	if len(req.Attributes) > maxUsageAttributes {
		return extensionUsageDropReasonAttributeCountExceeded, status.Errorf(codes.InvalidArgument,
			"event declares more than %d attributes", maxUsageAttributes)
	}

	for key := range req.Attributes {
		if key == "" || len(key) > maxUsageKeyBytes {
			return extensionUsageDropReasonAttributeKeyInvalid, status.Errorf(codes.InvalidArgument,
				"attribute keys are required and must be at most %d UTF-8 bytes",
				maxUsageKeyBytes)
		}
	}

	for key, value := range req.Attributes {
		if len(value) > maxUsageValueBytes {
			return extensionUsageDropReasonAttributeValueTooLong, status.Errorf(codes.InvalidArgument,
				"attribute value for key %q must be at most %d UTF-8 bytes",
				key, maxUsageValueBytes)
		}
	}

	return "", nil
}
