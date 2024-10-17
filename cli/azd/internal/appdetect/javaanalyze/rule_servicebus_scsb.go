package javaanalyze

import (
	"fmt"
	"strings"
)

type ruleServiceBusScsb struct {
	javaProject *javaProject
}

func (r *ruleServiceBusScsb) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-stream-binder-servicebus" {
				r.javaProject = javaProject
				return true
			}
		}
	}
	return false
}

// Function to find all properties that match the pattern `spring.cloud.stream.bindings.<binding-name>.destination`
func findBindingDestinations(properties map[string]string) map[string]string {
	result := make(map[string]string)

	// Iterate through the properties map and look for matching keys
	for key, value := range properties {
		// Check if the key matches the pattern `spring.cloud.stream.bindings.<binding-name>.destination`
		if strings.HasPrefix(key, "spring.cloud.stream.bindings.") && strings.HasSuffix(key, ".destination") {
			// Extract the binding name
			bindingName := key[len("spring.cloud.stream.bindings.") : len(key)-len(".destination")]
			// Store the binding name and destination value
			result[bindingName] = fmt.Sprintf("%v", value)
		}
	}

	return result
}

func (r *ruleServiceBusScsb) apply(azureYaml *AzureYaml) {
	bindingDestinations := findBindingDestinations(r.javaProject.springProject.applicationProperties)
	destinations := make([]string, 0, len(bindingDestinations))
	for bindingName, destination := range bindingDestinations {
		destinations = append(destinations, destination)
		fmt.Printf("Service Bus queue [%s] found for binding [%s]", destination, bindingName)
	}
	resource := ServiceBusResource{
		Resource: Resource{
			Name: "Azure Service Bus",
			Type: "Azure Service Bus",
		},
		Queues: destinations,
	}
	azureYaml.Resources = append(azureYaml.Resources, &resource)

	azureYaml.ServiceBindings = append(azureYaml.ServiceBindings, ServiceBinding{
		Name:     "Azure Service Bus",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
