// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const inspectorHelpFooter = `Local Agent Context:
  Start a local Foundry agent separately with 'azd ai agent run'.
  The inspector targets localhost:8088 and serves its browser UI on port 8087.
  Use --port to change the target agent port and --inspector-port to change
  the UI port. These are local ports, not a deployed Foundry project endpoint.

  The inspector does not resolve FOUNDRY_PROJECT_ENDPOINT or load agent
  runtime environment variables. Configure the agent in the terminal or
  project where it is started; selecting an azd environment here does not
  start or reconfigure that agent.

  Use --session-id and --conversation-id to seed the browser session.
  When omitted, the browser UI creates fresh IDs. Press Ctrl+C to stop
  the inspector.

Learn More:
  Local agent development: 'azd ai agent run --help'
  Inspector options: 'azd ai inspector launch --help'`
