package project

import "fmt"

type ServiceLanguageKind string

const (
	ServiceLanguageNone       ServiceLanguageKind = ""
	ServiceLanguageDotNet     ServiceLanguageKind = "dotnet"
	ServiceLanguageCsharp     ServiceLanguageKind = "csharp"
	ServiceLanguageFsharp     ServiceLanguageKind = "fsharp"
	ServiceLanguageJavaScript ServiceLanguageKind = "js"
	ServiceLanguageTypeScript ServiceLanguageKind = "ts"
	ServiceLanguagePython     ServiceLanguageKind = "python"
	ServiceLanguageJava       ServiceLanguageKind = "java"
	ServiceLanguageDocker     ServiceLanguageKind = "docker"
	ServiceLanguageSwa        ServiceLanguageKind = "swa"
)

type ServiceTargetKind string

const (
	NonSpecifiedTarget       ServiceTargetKind = ""
	AppServiceTarget         ServiceTargetKind = "appservice"
	ContainerAppTarget       ServiceTargetKind = "containerapp"
	AzureFunctionTarget      ServiceTargetKind = "function"
	StaticWebAppTarget       ServiceTargetKind = "staticwebapp"
	SpringAppTarget          ServiceTargetKind = "springapp"
	AksTarget                ServiceTargetKind = "aks"
	DotNetContainerAppTarget ServiceTargetKind = "containerapp-dotnet"
	AiEndpointTarget         ServiceTargetKind = "ai.endpoint"
)

func parseServiceLanguage(kind ServiceLanguageKind) (ServiceLanguageKind, error) {
	// aliases
	if string(kind) == "py" {
		return ServiceLanguagePython, nil
	}

	switch kind {
	case ServiceLanguageNone,
		ServiceLanguageDotNet,
		ServiceLanguageCsharp,
		ServiceLanguageFsharp,
		ServiceLanguageJavaScript,
		ServiceLanguageTypeScript,
		ServiceLanguagePython,
		ServiceLanguageJava:
		// Excluding ServiceLanguageDocker and ServiceLanguageSwa since it is implicitly derived currently,
		// and not an actual language
		return kind, nil
	}

	return ServiceLanguageKind("Unsupported"), fmt.Errorf("unsupported language '%s'", kind)
}

func parseServiceHost(kind ServiceTargetKind) (ServiceTargetKind, error) {
	switch kind {

	// NOTE: We do not support DotNetContainerAppTarget as a listed service host type in azure.yaml, hence
	// it not include in this switch statement. We should think about if we should support this in azure.yaml because
	// presently it's the only service target that is tied to a language.
	case AppServiceTarget,
		ContainerAppTarget,
		AzureFunctionTarget,
		StaticWebAppTarget,
		SpringAppTarget,
		AksTarget,
		AiEndpointTarget:

		return kind, nil
	}

	return ServiceTargetKind(""), fmt.Errorf("unsupported host '%s'", kind)
}

// ServiceLifecycleEventArgs are the event arguments available when
// any service lifecycle event has been triggered
type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}
