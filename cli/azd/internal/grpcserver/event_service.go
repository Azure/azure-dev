package grpcserver

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
)

// MyEventServiceServer implements azdext.EventServiceServer.
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
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		switch msg.MessageType.(type) {
		case *azdext.EventMessage_Subscribe:
			subscribeMsg := msg.GetSubscribe()
			for _, eventName := range subscribeMsg.EventNames {
				evt := ext.Event(eventName)

				s.projectConfig.AddHandler(evt, func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
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
						log.Printf("error sending event: %s", err.Error())
						return err
					}

					return nil
				})
			}
		}
	}
}
