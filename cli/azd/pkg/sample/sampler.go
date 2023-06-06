package sample

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/progress"
)

type Sampler struct {
	// Import publisher to enable message sending
	publisher messaging.Publisher
}

type SampleResult struct {
	Value string
}

func NewSampler(publisher messaging.Publisher) *Sampler {
	return &Sampler{
		publisher: publisher,
	}
}

func (s *Sampler) LongRunningOperation(ctx context.Context) (*SampleResult, error) {
	// Send message payloads during long running operation
	for i := 1; i <= 5; i++ {
		err := s.publisher.Send(ctx, progress.NewProgressMessage(fmt.Sprintf("Sampling Project %d", i), progress.Running))
		if err != nil {
			return nil, err
		}
		time.Sleep(2 * time.Second)
		err = s.publisher.Send(ctx, progress.NewProgressMessage(fmt.Sprintf("Sampling Project %d", i), progress.Success))
		if err != nil {
			return nil, err
		}

	}

	return &SampleResult{}, nil
}
