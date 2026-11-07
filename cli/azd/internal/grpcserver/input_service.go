// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type inputProviderEntry struct {
	kind      string
	extension string
	broker    *grpcbroker.MessageBroker[v1beta.InputMessage]
	err       error
}

// InputService coordinates immediate string input resolution over the beta channel.
type InputService struct {
	v1beta.UnimplementedInputServiceServer
	extensionManager *extensions.Manager
	mu               sync.RWMutex
	providers        []inputProviderEntry
}

// NewInputService creates a coordinator that does not require an initialized environment.
func NewInputService(extensionManager *extensions.Manager) *InputService {
	return &InputService{extensionManager: extensionManager}
}

// HasInputProviders reports whether any provider has registered, including failed streams.
func (s *InputService) HasInputProviders(context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.providers) > 0, nil
}

// Stream accepts capability-authorized provider registrations and handles correlated requests.
func (s *InputService) Stream(stream v1beta.InputService_StreamServer) error {
	claims, err := extensions.GetClaimsFromContext(stream.Context())
	if err != nil {
		return err
	}
	extension, err := s.extensionManager.GetInstalled(extensions.FilterOptions{Id: claims.Subject})
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "getting input provider extension: %s", err)
	}
	if !extension.HasCapability(extensions.InputProviderCapability) {
		return status.Error(codes.PermissionDenied, "extension does not declare input-provider capability")
	}
	broker := grpcbroker.NewMessageBroker(stream, azdext.NewBetaInputEnvelope(), extension.Id, log.Default())
	defer func() {
		s.mu.Lock()
		for i := range s.providers {
			if s.providers[i].broker == broker {
				s.providers[i].err = status.Error(codes.Unavailable, "input provider disconnected")
			}
		}
		s.mu.Unlock()
	}()
	if err := broker.On(func(
		_ context.Context, request *v1beta.RegisterInputProviderRequest,
	) (*v1beta.InputMessage, error) {
		return s.registerProvider(extension.Id, broker, request)
	}); err != nil {
		return err
	}
	if err := broker.Run(stream.Context()); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("input provider stream: %w", err)
	}
	return nil
}

func (s *InputService) registerProvider(
	extensionID string, broker *grpcbroker.MessageBroker[v1beta.InputMessage],
	request *v1beta.RegisterInputProviderRequest,
) (*v1beta.InputMessage, error) {
	if len(request.Kinds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one input kind is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isInputBrokerClosed(broker) {
		return nil, status.Error(codes.Unavailable, "input provider stream is closed")
	}
	seen := make(map[string]bool)
	for _, kind := range request.Kinds {
		if strings.TrimSpace(kind) == "" || seen[kind] {
			return nil, status.Error(codes.InvalidArgument, "input kinds must be nonempty and unique")
		}
		seen[kind] = true
		for _, entry := range s.providers {
			if entry.kind == kind && entry.extension == extensionID && entry.err == nil &&
				!isInputBrokerClosed(entry.broker) {
				return nil, status.Errorf(codes.AlreadyExists, "input kind %q is already registered", kind)
			}
		}
	}
	for _, kind := range request.Kinds {
		s.providers = slices.DeleteFunc(s.providers, func(entry inputProviderEntry) bool {
			return entry.kind == kind && entry.extension == extensionID
		})
		s.providers = append(s.providers, inputProviderEntry{kind: kind, extension: extensionID, broker: broker})
	}
	return &v1beta.InputMessage{
		MessageType: &v1beta.InputMessage_RegisterInputProviderResponse{
			RegisterInputProviderResponse: &v1beta.RegisterInputProviderResponse{},
		},
	}, nil
}

// ResolveInput gathers options deterministically and intersects required sets.
// Recommendations do not constrain other providers. No matches or all declines return nil.
func (s *InputService) ResolveInput(ctx context.Context, definition *v1beta.InputDefinition) ([]inputs.Option, error) {
	if definition.GetId() == "" || definition.GetKind() == "" {
		return nil, errors.New("input ID and semantic kind are required")
	}
	s.mu.RLock()
	providers := slices.Clone(s.providers)
	s.mu.RUnlock()
	slices.SortFunc(providers, func(a, b inputProviderEntry) int {
		return cmp.Compare(a.extension, b.extension)
	})

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var contributions []inputs.Contribution
	for _, provider := range providers {
		if provider.kind != definition.Kind {
			continue
		}
		if provider.err != nil {
			return nil, fmt.Errorf("extension %q resolving input %q: %w",
				provider.extension, definition.Id, provider.err)
		}
		response, err := provider.broker.SendAndWait(ctx, &v1beta.InputMessage{
			RequestId: uuid.NewString(),
			MessageType: &v1beta.InputMessage_ResolveInputRequest{
				ResolveInputRequest: &v1beta.ResolveInputRequest{Input: definition},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("extension %q resolving input %q: %w", provider.extension, definition.Id, err)
		}
		resolved := response.GetResolveInputResponse()
		if resolved == nil {
			return nil, fmt.Errorf("extension %q returned an invalid input response", provider.extension)
		}
		contributions = append(contributions, inputs.Contribution{Source: provider.extension, Response: resolved})
	}
	return inputs.Combine(definition, contributions)
}

func isInputBrokerClosed(broker *grpcbroker.MessageBroker[v1beta.InputMessage]) bool {
	select {
	case <-broker.Done():
		return true
	default:
		return false
	}
}
