// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

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

type FrameworkRequirements struct {
	Package FrameworkPackageRequirements
}

type FrameworkPackageRequirements struct {
	RequireRestore bool
	RequireBuild   bool
}

// FrameworkService is an abstraction for a programming language or framework
// that describe the required tools as well as implementations for
// restore and build commands
type FrameworkService interface {
	// Gets a list of the required external tools for the framework service
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool

	// Initializes the framework service for the specified service configuration
	// This is useful if the framework needs to subscribe to any service events
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// Gets the requirements for the language or framework service.
	// This enables more fine grain control on whether the language / framework
	// supports or requires lifecycle commands such as restore, build, and package
	Requirements() FrameworkRequirements

	// Restores dependencies for the framework service
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		progress *async.Progress[ServiceProgress],
	) (*ServiceRestoreResult, error)

	// Builds the source for the framework service
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		restoreOutput *ServiceRestoreResult,
		progress *async.Progress[ServiceProgress],
	) (*ServiceBuildResult, error)

	// Packages the source suitable for deployment
	// This may optionally perform a rebuild internally depending on the language/framework requirements
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		buildOutput *ServiceBuildResult,
		progress *async.Progress[ServiceProgress],
	) (*ServicePackageResult, error)
}

// CompositeFrameworkService is a framework service that requires a nested
// framework service. An example would be a Docker project that uses another
// framework services such as NPM or Python as a dependency. This supports
// local inner-loop as well as release restore & package support.
type CompositeFrameworkService interface {
	FrameworkService
	SetSource(inner FrameworkService)
}

func validatePackageOutput(packagePath string) error {
	entries, err := os.ReadDir(packagePath)
	if err != nil && os.IsNotExist(err) {
		return fmt.Errorf("package output '%s' does not exist, %w", packagePath, err)
	} else if err != nil {
		return fmt.Errorf("failed to read package output '%s', %w", packagePath, err)
	} else if len(entries) == 0 {
		return fmt.Errorf("package output '%s' is empty", packagePath)
	}

	return nil
}

func (slk ServiceLanguageKind) IsDotNet() bool {
	return slk == ServiceLanguageDotNet || slk == ServiceLanguageCsharp || slk == ServiceLanguageFsharp
}
