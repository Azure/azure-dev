// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "fmt"

func agentAddedMessage(agentName, serviceName string) string {
	if agentName == serviceName {
		return fmt.Sprintf("\nAdded agent '%s' to azure.yaml.\n", agentName)
	}

	return fmt.Sprintf(
		"\nAdded agent '%s' to azure.yaml as service '%s'.\n",
		agentName,
		serviceName,
	)
}

func printAgentAddedMessage(agentName, serviceName string) {
	fmt.Print(agentAddedMessage(agentName, serviceName))
}
