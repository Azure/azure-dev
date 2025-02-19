package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
)

// eventService implements azdext.EventServiceServer.
type eventService struct {
	azdext.UnimplementedEventServiceServer
	projectConfig *project.ProjectConfig
}

func NewEventService(projectConfig *project.ProjectConfig) azdext.EventServiceServer {
	return &eventService{
		projectConfig: projectConfig,
	}
}

// EventStream handles bidirectional streaming.
func (s *eventService) EventStream(stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage]) error {
	// ctx := stream.Context()

	// extensionClaims, err := extensions.GetTokenClaims(ctx)
	// if err != nil {
	// 	return fmt.Errorf("failed to get token claims: %w", err)
	// }

	// // Retrieve the "authorization" token from the incoming context metadata.
	// md, ok := metadata.FromIncomingContext(ctx)
	// if !ok {
	// 	// ...handle missing metadata...
	// }

	// authToken := ""
	// if authHeaders := md.Get("authorization"); len(authHeaders) > 0 {
	// 	authToken = authHeaders[0]
	// }

	// fmt.Printf("Received auth token: %s\n", authToken)

	// Declare local map and mutex for status channels before entering the receive loop.
	statusChans := make(map[string]chan string)
	var mu sync.Mutex

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		switch msg.MessageType.(type) {
		case *azdext.EventMessage_Subscribe:
			subscribeMsg := msg.GetSubscribe()
			for _, eventName := range subscribeMsg.EventNames {
				// Create a channel for this event.
				mu.Lock()
				statusChans[eventName] = make(chan string, 1)
				mu.Unlock()

				evt := ext.Event(eventName)
				err := s.projectConfig.AddHandler(
					evt,
					func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
						// Send invoke message to the extension.
						err := stream.Send(&azdext.EventMessage{
							MessageType: &azdext.EventMessage_Invoke{
								Invoke: &azdext.InvokeMessage{
									EventName: eventName,
									Args: map[string]string{
										"Name": s.projectConfig.Name,
										"Path": s.projectConfig.Path,
									},
								},
							},
						})
						if err != nil {
							return err
						}

						// Wait for a status message on the dedicated channel.
						mu.Lock()
						ch, ok := statusChans[eventName]
						mu.Unlock()
						if !ok {
							return fmt.Errorf("no status channel for event: %s", eventName)
						}

						status := <-ch

						// Clean up the channel.
						mu.Lock()
						delete(statusChans, eventName)
						mu.Unlock()

						if status == "failed" {
							return errors.New("extension hook failed")
						}

						return nil
					})

				if err != nil {
					return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
				}
			}
		case *azdext.EventMessage_Status:
			statusMessage := msg.GetStatus()

			mu.Lock()
			ch, ok := statusChans[statusMessage.EventName]
			mu.Unlock()

			if ok {
				ch <- statusMessage.Status
			}
		}
	}
}
