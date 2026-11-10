// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

type preflightReports struct {
	client *azdext.AzdClient
	mu     sync.Mutex
	text   []string
}

func (r *preflightReports) add(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.text = append(r.text, text)
}

func (r *preflightReports) postProvision(
	ctx context.Context, request *v1beta.InvokeProjectHandler,
) (*v1beta.EventMessage, error) {
	if request.GetEventName() != "postprovision" || request.GetInvocationId() == "" {
		return nil, fmt.Errorf("invalid postprovision report invocation")
	}
	r.mu.Lock()
	text := strings.Join(r.text, "\n")
	r.text = nil
	r.mu.Unlock()

	if text != "" {
		if _, err := r.client.CommandResult().SetFollowUp(ctx, &v1beta.SetFollowUpRequest{
			InvocationId: request.GetInvocationId(),
			Text:         "BYO-AKS preflight report:\n" + text,
		}); err != nil {
			return nil, fmt.Errorf("publish BYO-AKS preflight report: %w\nBYO-AKS preflight report:\n%s", err, text)
		}
	}
	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: request.GetEventName(),
				Status:    "completed",
			},
		},
	}, nil
}

func newListenCommand(configure func(*azdext.ExtensionHost, func(string))) *cobra.Command {
	command := azdext.NewListenCommand(nil)
	command.RunE = func(command *cobra.Command, _ []string) error {
		ctx, cancel := context.WithCancel(azdext.WithAccessToken(command.Context()))
		defer cancel()
		client, err := azdext.NewAzdClient()
		if err != nil {
			return fmt.Errorf("failed to create azd client: %w", err)
		}
		defer client.Close()
		reports := &preflightReports{client: client}
		stream, err := client.EventsBeta().EventStream(ctx)
		if err != nil {
			return fmt.Errorf("open postprovision report event stream: %w", err)
		}
		broker := grpcbroker.NewMessageBroker(stream, &preflightEventEnvelope{}, "azure.ai.projects", nil)
		defer broker.Close()
		if err := broker.On(reports.postProvision); err != nil {
			return fmt.Errorf("register postprovision report handler: %w", err)
		}
		var workers sync.WaitGroup
		failures := make(chan error, 2)
		workers.Go(func() {
			err := broker.Run(ctx)
			if ctx.Err() == nil && err == nil {
				err = fmt.Errorf("postprovision report event stream closed unexpectedly")
			}
			failures <- err
		})
		defer func() {
			cancel()
			workers.Wait()
		}()
		registrationCtx, registrationCancel := context.WithTimeout(ctx, 5*time.Second)
		defer registrationCancel()
		if err := broker.Ready(registrationCtx); err != nil {
			return fmt.Errorf("prepare postprovision report event stream: %w", err)
		}
		response, err := broker.SendAndWait(registrationCtx, &v1beta.EventMessage{
			RequestId: uuid.NewString(),
			MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{EventNames: []string{"postprovision"}},
			},
		})
		if err != nil {
			return fmt.Errorf("subscribe to postprovision reports: %w", err)
		}
		if response.GetSubscribeProjectEventResponse() == nil {
			return fmt.Errorf("invalid postprovision report subscription response")
		}
		host := azdext.NewExtensionHost(client)
		configure(host, reports.add)
		workers.Go(func() { failures <- host.Run(ctx) })
		err = <-failures
		if command.Context().Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return command
}

type preflightEventEnvelope struct{}

func (*preflightEventEnvelope) GetRequestId(_ context.Context, message *v1beta.EventMessage) string {
	return message.GetRequestId()
}

func (*preflightEventEnvelope) SetRequestId(_ context.Context, message *v1beta.EventMessage, id string) {
	message.RequestId = id
}

func (*preflightEventEnvelope) GetError(message *v1beta.EventMessage) error {
	if message.GetError() == nil {
		return nil
	}
	wire, err := proto.Marshal(message.GetError())
	if err != nil {
		return fmt.Errorf("decode preflight event error: %w", err)
	}
	stable := new(azdext.ExtensionError)
	if err := proto.Unmarshal(wire, stable); err != nil {
		return fmt.Errorf("decode preflight event error: %w", err)
	}
	return azdext.UnwrapError(stable)
}

func (*preflightEventEnvelope) SetError(message *v1beta.EventMessage, err error) {
	value := new(v1beta.ExtensionError)
	wire, encodeErr := proto.Marshal(azdext.WrapError(err))
	if encodeErr != nil {
		value.Message = fmt.Sprintf("%s; encode postprovision error: %s", err, encodeErr)
	} else if decodeErr := proto.Unmarshal(wire, value); decodeErr != nil {
		value.Message = fmt.Sprintf("%s; encode postprovision error: %s", err, decodeErr)
	}
	message.MessageType = &v1beta.EventMessage_ProjectHandlerStatus{
		ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
			EventName: "postprovision",
			Status:    "failed",
			Message:   err.Error(),
			Error:     value,
		},
	}
}

func (*preflightEventEnvelope) GetInnerMessage(message *v1beta.EventMessage) any {
	switch content := message.GetMessageType().(type) {
	case *v1beta.EventMessage_InvokeProjectHandler:
		return content.InvokeProjectHandler
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return content.SubscribeProjectEventResponse
	default:
		return nil
	}
}

func (*preflightEventEnvelope) IsProgressMessage(*v1beta.EventMessage) bool { return false }

func (*preflightEventEnvelope) GetProgressMessage(*v1beta.EventMessage) string { return "" }

func (*preflightEventEnvelope) CreateProgressMessage(string, string) *v1beta.EventMessage { return nil }
