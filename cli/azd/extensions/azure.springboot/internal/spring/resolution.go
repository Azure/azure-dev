// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// DeploymentInputs contains deliberate deployment choices, never an ambient environment.
type DeploymentInputs struct {
	Profiles          []string
	Properties        map[string]string
	ApplicationInputs map[string]ApplicationInputKind
}

var applicationInputName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var deploymentOwnedIndexedSettings = []string{
	"spring.autoconfigure.exclude",
	"spring.config.additional-location",
	"spring.config.import",
	"spring.config.location",
	"spring.profiles.active",
	"spring.profiles.default",
	"spring.profiles.include",
}

var deploymentOwnedRuntimeSettings = func() []string {
	result := []string{
		"spring.ai.azure.openai.api-key",
		"spring.ai.azure.openai.chat.options.deployment-name",
		"spring.ai.azure.openai.embedding.options.deployment-name",
		"spring.ai.azure.openai.endpoint",
		"spring.ai.azure.openai.openai-api-key",
		"spring.datasource.driver-class-name",
		"spring.datasource.password",
		"spring.datasource.url",
		"spring.datasource.username",
		"spring.jpa.hibernate.ddl-auto",
		"spring.sql.init.mode",
	}
	result = append(result, deploymentOwnedFoundrySelectors...)
	return append(result, deploymentOwnedDatasourceSelectors...)
}()

var reservedApplicationInputs = func() map[string]bool {
	result := map[string]bool{
		"_JAVA_OPTIONS":                          true,
		"AZURE_OPENAI_API_KEY":                   true,
		"AZURE_OPENAI_CHAT_DEPLOYMENT_NAME":      true,
		"AZURE_OPENAI_EMBEDDING_DEPLOYMENT_NAME": true,
		"AZURE_OPENAI_ENDPOINT":                  true,
		"JAVA_TOOL_OPTIONS":                      true,
		"JDK_JAVA_OPTIONS":                       true,
		"MYSQL_DATABASE":                         true,
		"MYSQL_HOST":                             true,
		"MYSQL_PASSWORD":                         true,
		"MYSQL_PORT":                             true,
		"MYSQL_USERNAME":                         true,
		"POSTGRES_DATABASE":                      true,
		"POSTGRES_HOST":                          true,
		"POSTGRES_PASSWORD":                      true,
		"POSTGRES_PORT":                          true,
		"POSTGRES_USERNAME":                      true,
		"SPRING_APPLICATION_JSON":                true,
	}
	for _, setting := range deploymentOwnedHostingSettings {
		for _, name := range environmentPropertyNames(setting)[1:] {
			result[name] = true
		}
	}
	for _, setting := range deploymentOwnedConfigurationSettings {
		for _, name := range environmentPropertyNames(setting)[1:] {
			result[name] = true
		}
	}
	for _, setting := range deploymentOwnedRuntimeSettings {
		for _, name := range environmentPropertyNames(setting)[1:] {
			result[name] = true
		}
	}
	for _, setting := range unsupportedDatasourceSettings {
		for _, name := range environmentPropertyNames(setting.key)[1:] {
			result[name] = true
		}
	}
	return result
}()

var reservedIndexedApplicationInputs = func() map[string]bool {
	result := map[string]bool{}
	for _, setting := range deploymentOwnedIndexedSettings {
		for _, name := range environmentPropertyNames(setting)[1:] {
			result[name] = true
		}
	}
	return result
}()

// Resource describes a logical dependency independently of Azure server placement.
type Resource struct {
	ID          string
	Kind        ResourceKind
	Name        string
	Disposition Disposition
}

// Disposition distinguishes provisioned resources from application-local storage.
type Disposition string

const (
	// Provisioned requires an Azure resource provider.
	Provisioned Disposition = "provisioned"
	// ApplicationLocal requires no separately provisioned resource.
	ApplicationLocal Disposition = "applicationLocal"
)

// Binding connects an application's named dependency to a logical resource.
type Binding struct {
	ID             string
	ApplicationID  string
	ResourceID     string
	PropertyPrefix string
	Authentication string
	JDBCOptions    string
	Driver         string
	Embedded       *EmbeddedBinding
	Foundry        *FoundryRequirement
	Evidence       []Evidence
}

// DecisionKind distinguishes missing values from evidenced alternative selections.
type DecisionKind string

const (
	// MissingInput requires a value without guessing one from dependencies.
	MissingInput DecisionKind = "missingInput"
	// Ambiguous requires selecting among evidenced alternatives.
	Ambiguous DecisionKind = "ambiguous"
)

// Decision identifies an unresolved selector and the scope it can affect.
type Decision struct {
	ID             string
	Kind           DecisionKind
	Input          string
	Profiles       bool
	Choices        []string
	ApplicationIDs []string
	BindingIDs     []string
	Evidence       []Evidence
}

// Diagnostic describes a blocking invalid or unsupported application configuration.
type Diagnostic struct {
	Kind    string
	Message string
	Repairs []CredentialRepair
}

func (d *Diagnostic) Error() string { return d.Message }

func invalidConfiguration(message string, args ...any) error {
	return &Diagnostic{Kind: "invalid", Message: fmt.Sprintf(message, args...)}
}

func unsupportedConfiguration(message string, args ...any) error {
	return &Diagnostic{Kind: "unsupported", Message: fmt.Sprintf(message, args...)}
}

// Analysis separates resolved resource facts from decisions and blocking diagnostics.
type Analysis struct {
	Applications []Application
	Resources    []Resource
	Bindings     []Binding
	Decisions    []Decision
	Diagnostics  []Diagnostic
}

type missingInput struct {
	name     string
	evidence []Evidence
	choices  []string
}

func (m *missingInput) Error() string {
	return fmt.Sprintf("a required Spring configuration placeholder %s is unresolved", m.name)
}

// AnalyzeProject resolves supported resources using only explicit deployment inputs.
// The detector supports one application/datasource; identities do not imply broader detection.
func AnalyzeProject(
	ctx context.Context, directory string, resolver ModelResolver, inputs DeploymentInputs,
) (*Analysis, error) {
	result := &Analysis{Applications: []Application{{ID: "app"}}}
	applicationInputs, err := resolveApplicationInputs(inputs.ApplicationInputs)
	if err != nil {
		return nil, err
	}
	properties := make(map[string]string, len(inputs.Properties)+1)
	for key, value := range inputs.Properties {
		if !propertyName.MatchString(key) {
			return nil, errors.New("deployment property names must be Spring property or placeholder identifiers")
		}
		name := canonical(key)
		if _, exists := properties[name]; exists {
			return nil, fmt.Errorf("deployment property %s is supplied more than once", key)
		}
		properties[name] = value
	}
	if _, exists := properties[canonical("spring.profiles.active")]; exists {
		return nil, errors.New("use --profiles for spring.profiles.active")
	}
	if inputs.Profiles != nil {
		if len(inputs.Profiles) == 0 || len(inputs.Profiles) > 64 {
			return nil, errors.New("--profiles requires 1-64 valid profile names")
		}
		for _, profile := range inputs.Profiles {
			if !profileName.MatchString(profile) {
				return nil, errors.New("--profiles requires valid, nonempty profile names")
			}
		}
		properties[canonical("spring.profiles.active")] = strings.Join(inputs.Profiles, ",")
	}
	used := map[string]bool{}
	lookup := func(key string) (string, bool) {
		key = canonical(key)
		value, ok := properties[key]
		if ok {
			used[key] = true
		}
		return value, ok
	}
	application, err := Analyze(ctx, directory, resolver, lookup)
	if err != nil {
		if missing, ok := errors.AsType[*missingInput](err); ok {
			decision := Decision{
				ID: "property:" + missing.name, Kind: MissingInput, Input: missing.name,
				ApplicationIDs: []string{"app"}, Evidence: deploymentEvidence(missing.evidence),
			}
			for _, evidence := range missing.evidence {
				if evidence.Property == "spring.profiles.active" || evidence.Property == "spring.profiles.default" {
					decision.ID, decision.Profiles = "profiles", true
					decision.Choices = missing.choices
					if len(decision.Choices) > 1 {
						decision.Kind = Ambiguous
					}
				}
				if evidence.Property == "spring.datasource.url" {
					decision.BindingIDs = []string{"app/datasource:primary"}
					result.Bindings = []Binding{{
						ID: "app/datasource:primary", ApplicationID: "app",
						PropertyPrefix: "spring.datasource", Evidence: decision.Evidence,
					}}
				}
			}
			if !decision.Profiles && !slices.ContainsFunc(missing.evidence, func(e Evidence) bool {
				return e.Property == "spring.datasource.url" || e.Property == "server.port"
			}) {
				result.Diagnostics = []Diagnostic{{
					Kind: "unsupported",
					Message: "unresolved configuration outside datasource URL, server port, or profile selection; " +
						"configure it in the application",
				}}
			} else {
				result.Decisions = []Decision{decision}
			}
			return result, nil
		}
		if diagnostic, ok := errors.AsType[*Diagnostic](err); ok {
			result.Diagnostics = []Diagnostic{*diagnostic}
			return result, nil
		}
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(inputs.Properties)) {
		if !used[canonical(key)] {
			return nil, fmt.Errorf("deployment input %s cannot affect the selected configuration", key)
		}
	}
	generatedEvidence := slices.Clone(application.InputEvidence)
	ownershipEvidence := slices.Clone(application.InputOwnershipEvidence)
	for _, requirement := range application.Requirements {
		generatedEvidence = append(generatedEvidence, requirement.Trace...)
		ownershipEvidence = append(ownershipEvidence, requirement.Trace...)
	}
	if application.Embedded != nil {
		generatedEvidence = append(generatedEvidence, application.Embedded.Evidence...)
		ownershipEvidence = append(ownershipEvidence, application.Embedded.Evidence...)
	}
	if application.Foundry != nil {
		ownershipEvidence = append(ownershipEvidence, application.Foundry.Evidence...)
	}
	for _, input := range applicationInputs {
		keys := []string{canonical(input.Name)}
		if base, indexed := indexedApplicationInputBase(input.Name); indexed {
			keys = append(keys, canonical(base))
		}
		for _, key := range keys {
			for _, reference := range application.InputReferences[key] {
				if containsEvidence(ownershipEvidence, reference) {
					reference = outermostSupportedReference(
						reference,
						application.InputReferences,
						ownershipEvidence,
					)
					return nil, fmt.Errorf(
						"application input %s affects deployment-owned property %s in %s",
						input.Name, reference.Property, reference.Source)
				}
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(inputs.Properties)) {
		for _, reference := range application.InputReferences[canonical(key)] {
			if containsEvidence(generatedEvidence, reference) {
				continue
			}
			return nil, fmt.Errorf(
				"deployment input %s also affects unsupported property %s in %s; configure that setting in the application",
				key, reference.Property, reference.Source)
		}
	}
	// Only these destinations have deployment-owned runtime overrides.
	for key := range properties {
		if key == canonical("spring.profiles.active") {
			continue
		}
		if !slices.ContainsFunc(application.InputEvidence, func(e Evidence) bool {
			return canonical(e.Property) == key
		}) {
			return nil, errors.New("explicit deployment properties must resolve a datasource URL or server port; " +
				"configure other settings in the application")
		}
	}
	requirements, embedded, foundry := application.Requirements, application.Embedded, application.Foundry
	application.Requirements, application.Embedded, application.Foundry = nil, nil, nil
	application.ApplicationInputs = applicationInputs
	application.InputEvidence, application.InputOwnershipEvidence, application.InputReferences = nil, nil, nil
	result.Applications = []Application{*application}
	for _, requirement := range requirements {
		result.Resources = append(result.Resources, Resource{
			ID: "datasource:primary", Kind: requirement.Kind, Name: requirement.Name, Disposition: Provisioned,
		})
		result.Bindings = append(result.Bindings, Binding{
			ID: "app/datasource:primary", ApplicationID: "app", ResourceID: "datasource:primary",
			PropertyPrefix: "spring.datasource", Authentication: requirement.Authentication,
			JDBCOptions: requirement.JDBCOptions, Evidence: deploymentEvidence(requirement.Trace),
			Driver: requirement.Driver,
		})
	}
	if embedded != nil {
		result.Resources = append(result.Resources, Resource{
			ID: "datasource:primary", Kind: ResourceKind(embedded.Engine), Disposition: ApplicationLocal,
		})
		result.Bindings = append(result.Bindings, Binding{
			ID: "app/datasource:primary", ApplicationID: "app", ResourceID: "datasource:primary",
			PropertyPrefix: "spring.datasource", Embedded: embedded, Evidence: deploymentEvidence(embedded.Evidence),
		})
	}
	if foundry != nil {
		result.Resources = append(result.Resources, Resource{
			ID: "foundry:primary", Kind: Foundry, Name: "azure-openai", Disposition: Provisioned,
		})
		result.Bindings = append(result.Bindings, Binding{
			ID: "app/foundry:primary", ApplicationID: "app", ResourceID: "foundry:primary",
			PropertyPrefix: "spring.ai.azure.openai", Authentication: foundry.Authentication,
			Foundry: foundry, Evidence: deploymentEvidence(foundry.Evidence),
		})
	}
	return result, nil
}

func resolveApplicationInputs(inputs map[string]ApplicationInputKind) ([]ApplicationInput, error) {
	result := make([]ApplicationInput, 0, len(inputs))
	seen := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		kind := inputs[name]
		if !applicationInputName.MatchString(name) {
			return nil, fmt.Errorf("application input %q must be a valid environment variable name", name)
		}
		normalized := strings.ToUpper(name)
		if previous, exists := seen[normalized]; exists {
			return nil, fmt.Errorf("application inputs %s and %s differ only by case", previous, name)
		}
		seen[normalized] = name
		base, indexed := indexedApplicationInputBase(normalized)
		if reservedApplicationInputs[normalized] || indexed && reservedIndexedApplicationInputs[base] {
			return nil, fmt.Errorf("application input %s conflicts with a deployment-owned binding", name)
		}
		if kind != ApplicationInputValue && kind != ApplicationInputSecret {
			return nil, fmt.Errorf("application input %s must be classified as value or secret", name)
		}
		result = append(result, ApplicationInput{Name: name, Kind: kind})
	}
	return result, nil
}

func indexedApplicationInputBase(name string) (string, bool) {
	name = strings.TrimSuffix(name, "_")
	separator := strings.LastIndexByte(name, '_')
	if separator <= 0 || separator == len(name)-1 {
		return "", false
	}
	for _, char := range name[separator+1:] {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	return name[:separator], true
}

func containsEvidence(evidence []Evidence, reference Evidence) bool {
	return slices.ContainsFunc(evidence, func(candidate Evidence) bool {
		return canonical(candidate.Property) == canonical(reference.Property) &&
			candidate.Source == reference.Source
	})
}

func outermostSupportedReference(
	reference Evidence,
	references map[string][]Evidence,
	supportedEvidence []Evidence,
) Evidence {
	visited := map[string]bool{}
	for !visited[canonical(reference.Property)] {
		visited[canonical(reference.Property)] = true
		parents := references[canonical(reference.Property)]
		index := slices.IndexFunc(parents, func(candidate Evidence) bool {
			return containsEvidence(supportedEvidence, candidate)
		})
		if index < 0 {
			break
		}
		reference = parents[index]
	}
	return reference
}

func deploymentEvidence(evidence []Evidence) []Evidence {
	result := slices.Clone(evidence)
	for i := range result {
		if result[i].Source == "environment" {
			result[i].Source = "deployment input"
		}
	}
	return result
}

// ResolvedApplication validates the graph before adapting it to the single-app template.
func (a *Analysis) ResolvedApplication() (*Application, error) {
	if len(a.Decisions) != 0 || len(a.Diagnostics) != 0 {
		return nil, errors.New("deployment configuration has unresolved decisions or blocking diagnostics")
	}
	if len(a.Applications) != 1 || len(a.Bindings) > 2 || len(a.Resources) != len(a.Bindings) {
		return nil, errors.New(
			"the deployment template supports one application, at most one datasource, and at most one Foundry binding")
	}
	application := a.Applications[0]
	application.Requirements, application.Embedded, application.Foundry = nil, nil, nil
	resources := make(map[string]Resource, len(a.Resources))
	for _, resource := range a.Resources {
		if _, exists := resources[resource.ID]; exists {
			return nil, errors.New("the deployment graph contains duplicate resource identities")
		}
		resources[resource.ID] = resource
	}
	bindingIDs := make(map[string]bool, len(a.Bindings))
	for _, binding := range a.Bindings {
		if bindingIDs[binding.ID] {
			return nil, errors.New("the deployment graph contains duplicate binding identities")
		}
		bindingIDs[binding.ID] = true
		if binding.ApplicationID != application.ID {
			return nil, errors.New("the binding does not match the supported application")
		}
		resource, present := resources[binding.ResourceID]
		if !present {
			return nil, errors.New("the binding resource reference is invalid")
		}
		delete(resources, binding.ResourceID)
		switch binding.PropertyPrefix {
		case "spring.datasource":
			if binding.ID != application.ID+"/datasource:primary" || binding.ResourceID != "datasource:primary" {
				return nil, errors.New("the datasource binding does not match the supported application")
			}
			switch resource.Disposition {
			case ApplicationLocal:
				if binding.Embedded == nil || resource.Kind != ResourceKind(binding.Embedded.Engine) {
					return nil, errors.New("the embedded datasource binding is invalid")
				}
				application.Embedded = binding.Embedded
			case Provisioned:
				if binding.Embedded != nil || resource.Kind != MySQL && resource.Kind != PostgreSQL {
					return nil, errors.New("a provisioned datasource binding is invalid")
				}
				application.Requirements = []Requirement{{
					Kind: resource.Kind, Name: resource.Name, Authentication: binding.Authentication,
					JDBCOptions: binding.JDBCOptions, Driver: binding.Driver, Trace: binding.Evidence,
				}}
			default:
				return nil, errors.New("the datasource has no supported deployment disposition")
			}
		case "spring.ai.azure.openai":
			if binding.ID != application.ID+"/foundry:primary" || binding.ResourceID != "foundry:primary" ||
				resource.Kind != Foundry || resource.Disposition != Provisioned ||
				binding.Authentication != "apiKey" && binding.Authentication != "managedIdentity" ||
				binding.Embedded != nil || binding.Foundry == nil ||
				binding.Foundry.Authentication != binding.Authentication ||
				!validFoundryDeploymentNames(binding.Foundry) {
				return nil, errors.New("the Foundry binding is invalid")
			}
			if application.Foundry != nil {
				return nil, errors.New("multiple Foundry bindings are not supported")
			}
			foundry := *binding.Foundry
			foundry.Evidence = binding.Evidence
			application.Foundry = &foundry
		default:
			return nil, errors.New("the binding property namespace is not supported")
		}
	}
	if len(resources) != 0 {
		return nil, errors.New("the deployment graph contains an unbound resource")
	}
	return &application, nil
}

func validFoundryDeploymentNames(foundry *FoundryRequirement) bool {
	if foundry.ChatDeploymentName == "" && foundry.EmbeddingDeploymentName == "" {
		return false
	}
	if foundry.ChatDeploymentName != "" && !modelDeploymentName.MatchString(foundry.ChatDeploymentName) {
		return false
	}
	if foundry.EmbeddingDeploymentName != "" &&
		!modelDeploymentName.MatchString(foundry.EmbeddingDeploymentName) {
		return false
	}
	return foundry.ChatDeploymentName == "" ||
		foundry.EmbeddingDeploymentName == "" ||
		foundry.ChatDeploymentName != foundry.EmbeddingDeploymentName
}
