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

// runInspector launches the standalone Agent Inspector UI pointed at the
// local agent on a.flags.port. Validation in newInvokeCommand has
// already ensured --local is set and no message/input-file was provided.
func (a *InvokeAction) runInspector(ctx context.Context) error {
	// Route the inspector server's logs through the standard log package
	// so setupDebugLogging (--debug) controls where they go. Sharing one
	// prefixed logger with the browser-launch fallback below keeps all
	// inspector output greppable as "[inspector] ...".
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

	// Best-effort browser launch. Failures are non-fatal — the user
	// can still navigate manually using the URL printed above.
	go func() {
		if err := browser.OpenURL(url); err != nil {
			logger.Printf("failed to open browser: %v", err)
		}
	}()

	return srv.Start(ctx)
}
