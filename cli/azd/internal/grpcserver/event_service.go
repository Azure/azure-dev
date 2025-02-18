package grpcserver

import (
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
)

// MyEventServiceServer implements azdext.EventServiceServer.
type eventService struct {
	azdext.UnimplementedEventServiceServer
}

// EventStream handles bidirectional streaming.
func (s *eventService) EventStream(stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage]) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			// ...handle error...
			return err
		}
		log.Printf("Received message: %+v", msg)
		// Process message as needed, then send a response.
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
}
