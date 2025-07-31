package prompts

import (
	_ "embed"
)

//go:embed azd_plan_init.md
var AzdPlanInitPrompt string

//go:embed azd_iac_generation_rules.md
var AzdIacRulesPrompt string

//go:embed azure.yaml.json
var AzdYamlSchemaPrompt string
