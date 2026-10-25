// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

func validateIdentityInputs(node yaml.Node, service string) error {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	hosting, exists := fields["agentHosting"]
	if !exists {
		return nil
	}
	var inputs map[string]yaml.Node
	if err := hosting.Decode(&inputs); err != nil {
		return err
	}
	for _, name := range []string{"hostingManagementIdentityResourceId", "workloadIdentityResourceId"} {
		value, present := inputs[name]
		if !present {
			continue
		}
		var raw any
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("services.%s.agentHosting.%s: %w", service, name, err)
		}
		text, isString := raw.(string)
		if !isString || strings.TrimSpace(text) == "" {
			return fmt.Errorf("services.%s.agentHosting.%s: explicitly supplied identity must be a nonempty resource ID",
				service, name)
		}
	}
	return nil
}
