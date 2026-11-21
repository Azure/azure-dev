// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// AddProviderRegistry tracks add items on live extension streams.
type AddProviderRegistry struct {
	mu        sync.RWMutex // protects providers, keyed by authenticated extension ID
	providers map[string]*addProvider
}

// NewAddProviderRegistry creates an empty registry.
func NewAddProviderRegistry() *AddProviderRegistry {
	return &AddProviderRegistry{providers: make(map[string]*addProvider)}
}

// AddProviderItem is an add action offered by an extension.
type AddProviderItem struct {
	ID             string
	DisplayName    string
	Provider       string
	DisabledReason string
	owner          *addProvider
	itemID         string
}

// AttachOutput forwards the owning listener's output and returns a function that detaches it.
func (i AddProviderItem) AttachOutput(stdout, stderr io.Writer) func() {
	if i.owner == nil {
		return func() {}
	}

	extension := i.owner.extension
	// Use a common lock because the CLI may send both streams to the same writer.
	mu := new(sync.Mutex)
	out := &addOutputWriter{writer: stdout, mu: mu}
	errOut := &addOutputWriter{writer: stderr, mu: mu}
	extension.StdOut().AddWriter(out)
	extension.StdErr().AddWriter(errOut)
	return func() {
		extension.StdOut().RemoveWriter(out)
		extension.StdErr().RemoveWriter(errOut)
	}
}

type addOutputWriter struct {
	writer io.Writer
	mu     *sync.Mutex // serializes writes from both listener streams
}

func (w *addOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

// Run invokes this item on the stream that advertised it.
func (i AddProviderItem) Run(ctx context.Context) error {
	if i.DisabledReason != "" {
		return status.Error(codes.FailedPrecondition, i.DisabledReason)
	}
	if i.owner == nil {
		return status.Error(codes.FailedPrecondition, "add item is not registered")
	}
	return i.owner.run(ctx, i.itemID)
}

type addProvider struct {
	items     []AddProviderItem
	ctx       context.Context
	broker    *grpcbroker.MessageBroker[v1beta.AddProviderMessage]
	abort     context.CancelFunc
	ready     chan struct{}
	extension *extensions.Extension
}

func (p *addProvider) live() bool {
	if p.ctx.Err() != nil {
		return false
	}
	select {
	case <-p.broker.Done():
		return false
	default:
		return true
	}
}

// Items returns a snapshot of the add items on live streams.
func (r *AddProviderRegistry) Items() []AddProviderItem {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var items []AddProviderItem
	for _, provider := range r.providers {
		if provider.live() {
			items = append(items, provider.items...)
		}
	}
	slices.SortFunc(items, func(a, b AddProviderItem) int {
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return items
}

// HasProvider reports whether the extension registered a live stream, even with no items.
func (r *AddProviderRegistry) HasProvider(extensionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider := r.providers[extensionID]
	return provider != nil && provider.live()
}

func (r *AddProviderRegistry) register(extensionID string, provider *addProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.providers[extensionID]; current != nil && current.live() {
		return status.Error(codes.AlreadyExists, "extension already has an add provider")
	}
	r.providers[extensionID] = provider
	return nil
}

func (r *AddProviderRegistry) release(extensionID string, provider *addProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers[extensionID] == provider {
		delete(r.providers, extensionID)
	}
}

type addProviderService struct {
	v1beta.UnimplementedAddProviderServiceServer
	registry     *AddProviderRegistry
	getInstalled func(extensions.FilterOptions) (*extensions.Extension, error)
}

func (s *addProviderService) Stream(stream v1beta.AddProviderService_StreamServer) error {
	ctx := stream.Context()
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return err
	}
	extension, err := s.getInstalled(extensions.FilterOptions{Id: claims.Subject})
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "loading add provider extension: %v", err)
	}
	if !extension.HasCapability(extensions.AddProviderCapability) {
		return status.Error(codes.PermissionDenied, "extension lacks the add-provider capability")
	}

	message, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	request := message.GetRegisterRequest()
	if message.GetRequestId() == "" || message.GetError() != nil || request == nil {
		return status.Error(codes.InvalidArgument, "the first add provider message must be a correlated registration")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	broker := grpcbroker.NewMessageBroker(stream, &azdext.AddProviderEnvelope{}, extension.Id, nil)
	defer broker.Close()
	provider := &addProvider{
		ctx: ctx, broker: broker, abort: cancel, ready: make(chan struct{}), extension: extension,
	}
	seen := make(map[string]struct{}, len(request.GetItems()))
	for _, item := range request.GetItems() {
		id, displayName := item.GetId(), item.GetDisplayName()
		if strings.TrimSpace(id) == "" || strings.TrimSpace(displayName) == "" {
			return status.Error(codes.InvalidArgument, "add item ID and display name are required")
		}
		if _, exists := seen[id]; exists {
			return status.Errorf(codes.InvalidArgument, "duplicate add item ID %q", id)
		}
		seen[id] = struct{}{}
		provider.items = append(provider.items, AddProviderItem{
			ID: id, DisplayName: displayName, Provider: extension.Id, owner: provider, itemID: id,
			DisabledReason: strings.TrimSpace(item.GetDisabledReason()),
		})
	}
	if err := s.registry.register(extension.Id, provider); err != nil {
		return err
	}
	defer s.registry.release(extension.Id, provider)
	err = stream.Send(&v1beta.AddProviderMessage{
		RequestId: message.GetRequestId(),
		MessageType: &v1beta.AddProviderMessage_RegisterResponse{
			RegisterResponse: &v1beta.RegisterAddProviderResponse{},
		},
	})
	if err != nil {
		return err
	}
	close(provider.ready)

	// Returning the RPC cancels transport reads if a remote invocation fails to drain.
	streamDone := make(chan error, 1)
	go func() { streamDone <- broker.Run(ctx) }()
	select {
	case err := <-streamDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *addProvider) run(ctx context.Context, id string) error {
	// Keep the acknowledgement ahead of any invocation.
	select {
	case <-p.ready:
	case <-p.ctx.Done():
		return status.Error(codes.Unavailable, "add provider stream closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	if !p.live() {
		return status.Error(codes.Unavailable, "add provider stream closed")
	}
	response, err := p.broker.SendAndWait(ctx, &v1beta.AddProviderMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.AddProviderMessage_AddRequest{
			AddRequest: &v1beta.AddItemRequest{Id: id},
		},
	})
	if err != nil {
		if grpcbroker.IsCancellationUnacknowledged(err) {
			p.abort()
		}
		return extensions.WrapInvocationError(err, p.extension.Id, p.extension.Version, "")
	}
	if response.GetAddResponse() == nil {
		return status.Error(codes.Internal, "expected add item response")
	}
	return nil
}
