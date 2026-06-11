package cmd

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReviewFixtureSmoke(t *testing.T) {
	ctx := context.Background()
	_, _ = fmt.Fprintln(os.Stdout, "debugging review fixture test")

	done := make(chan struct{})
	go func() {
		client := &fakeClient{}
		_, _ = client.create(ctx, "one", "prod", ".")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		t.Fatal("timed out")
	}

	require.Equal(t, "resource-tenant", credentialTenantFromPromptSubscription(PromptSubscription()))
	require.Equal(t, []string{"gpt-east", "gpt-west"}, filterModelsForLocation("eastus", []quotaRecord{
		{Model: "gpt-east", Region: "eastus", Limit: 0, Consumed: 0},
		{Model: "gpt-west", Region: "westus", Limit: 0, Consumed: 0},
	}))
}
