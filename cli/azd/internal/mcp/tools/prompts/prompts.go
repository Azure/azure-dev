// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompts

import (
	_ "embed"
)

//go:embed azd_plan_init.md
var AzdPlanInitPrompt string

//go:embed azd_iac_generation_rules.md
var AzdIacRulesPrompt string

//go:embed azd_discovery_analysis.md
var AzdDiscoveryAnalysisPrompt string

//go:embed azd_architecture_planning.md
var AzdArchitecturePlanningPrompt string

//go:embed azd_azure_yaml_generation.md
var AzdAzureYamlGenerationPrompt string

//go:embed azd_infrastructure_generation.md
var AzdInfrastructureGenerationPrompt string

//go:embed azd_docker_generation.md
var AzdDockerGenerationPrompt string

//go:embed azd_project_validation.md
var AzdProjectValidationPrompt string

//go:embed azd_identify_user_intent.md
var AzdIdentifyUserIntentPrompt string

//go:embed azd_select_stack.md
var AzdSelectStackPrompt string

//go:embed azd_new_project.md
var AzdNewProjectPrompt string

//go:embed azd_modernize_project.md
var AzdModernizeProjectPrompt string

//go:embed azd_artifact_generation.md
var AzdArtifactGenerationPrompt string

//go:embed azd_appcode_generation.md
var AzdAppCodeGenerationPrompt string
