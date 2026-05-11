// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"azureaiagent/internal/inspector"

	"github.com/cli/browser"
)

// runInspector launches the inspector UI against the local agent.
func (a *InvokeAction) runInspector(ctx context.Context) error {
	logger := log.New(log.Writer(), "[inspector] ", log.LstdFlags)

	srv := inspector.New(inspector.Config{
		Port:      a.flags.inspectorPort,
		AgentPort: a.flags.port,
		Logger:    logger,
	})

	url := srv.URL()
	fmt.Printf("Inspector:    %s\n", url)
	fmt.Printf("Target:       localhost:%d (local)\n", a.flags.port)
	fmt.Println("\nPress Ctrl+C to stop the inspector.")

	ready := make(chan struct{})
	go func() {
		<-ready
		if err := browser.OpenURL(url); err != nil {
			logger.Printf("failed to open browser: %v", err)
		}
	}()

	return srv.Start(ctx, ready)
}
