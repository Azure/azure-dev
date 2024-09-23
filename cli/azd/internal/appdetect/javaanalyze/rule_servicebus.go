package javaanalyze

type ruleServiceBus struct {
}

func (mr *ruleServiceBus) Match(mavenProject *MavenProject) bool {
	if mavenProject.Dependencies != nil {
		for _, dep := range mavenProject.Dependencies {
			if dep.GroupId == "com.azure" && dep.ArtifactId == "" {
				return true
			}
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-stream-binder-servicebus" {
				return true
			}
		}
	}
	return false
}

func (mr *ruleServiceBus) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "Azure Service Bus",
		Type: "Azure Service Bus",
	})

	javaProject.ServiceBindings = append(javaProject.ServiceBindings, ServiceBinding{
		Name:     "Azure Service Bus",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
