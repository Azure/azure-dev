// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "fmt"

func agentAddedMessage(agentName string) string {
	return fmt.Sprintf("\nAdded agent '%s' to azure.yaml.\n", agentName)
}

func printAgentAddedMessage(agentName string) {
	fmt.Print(agentAddedMessage(agentName))
}
