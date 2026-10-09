// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"
)

// evaluatorSchemas indexes the published contract of every evaluator a group
// can reference.
//
// Built-ins have to be asked for separately. An unfiltered list returns only
// the project's own evaluators, so relying on it leaves every built-in without
// a schema and unable to check required evaluator-specific inputs.
//
// A failure is deliberately not fatal: without schemas the builder falls back
// to the agent-target shape, which is what it always used to send.
func (ec *evalContext) evaluatorSchemas(ctx context.Context) map[string]*eval_api.EvaluatorSummary {
	if ec.schemas != nil {
		return ec.schemas
	}
	index, err := ec.readEvaluatorSchemas(ctx)
	if len(index) == 0 {
		return nil
	}
	// Existing callers keep their best-effort behavior; explicit local sources
	// use the reader's error to refuse validation against an incomplete catalog.
	if err == nil {
		ec.schemas = index
	}
	return index
}

func (ec *evalContext) readEvaluatorSchemas(
	ctx context.Context,
) (map[string]*eval_api.EvaluatorSummary, error) {
	if ec.schemas != nil {
		return ec.schemas, nil
	}

	index := map[string]*eval_api.EvaluatorSummary{}
	var lookupErr error
	for _, filter := range []string{"", eval_api.EvaluatorTypeBuiltin} {
		list, err := ec.evalClient.ListEvaluators(ctx, filter, ProjectEndpointAPIVersion)
		if err != nil {
			lookupErr = errors.Join(lookupErr, err)
			continue
		}
		if list == nil {
			lookupErr = errors.Join(lookupErr, errors.New("the service returned no evaluator catalog"))
			continue
		}
		maps.Copy(index, list.ByName())
	}
	return index, lookupErr
}

// sampleBindings are the fields an agent target produces at run time. Anything
// an evaluator accepts that is not in this set has to come from a dataset
// column instead.
var sampleBindings = map[string]string{
	"response":         "{{sample.output_items}}",
	"tool_calls":       "{{sample.tool_calls}}",
	"tool_definitions": "{{sample.tool_definitions}}",
}

// sampleBindingsFor returns the run-time bindings a target of this kind can
// satisfy. An empty target kind means nothing is invoked, so nothing is bound.
//
// A model target supplies plain text, not an agent's structured tool output.
func sampleBindingsFor(targetType string) map[string]string {
	if targetType == project.TargetTypeAgent {
		return sampleBindings
	}
	if targetType == project.TargetTypeModel {
		return map[string]string{"response": "{{sample.output_text}}"}
	}
	return nil
}

// criterionPlan is the resolved binding for one evaluator.
type criterionPlan struct {
	dataMapping map[string]string
	initParams  map[string]any
	// itemFields are the fields sourced from dataset columns; they have to be
	// declared in the item schema.
	itemFields []string
}

// conversationField carries a whole conversation. The service rejects a
// mapping that pairs it with the turn-level fields:
//
//	Evaluator 'builtin.task_completion' has both 'messages' and
//	'query'/'response' in dataMapping. Use 'messages' for conversation-level
//	evaluation or 'query'/'response' for turn-level evaluation, but not both.
const conversationField = "messages"

// turnFields are the per-turn counterparts to conversationField.
var turnFields = []string{"query", "response"}

// defaultCriterionMapping is the standard item contract. Catalog properties
// describe accepted inputs, not columns guaranteed to exist in a source.
func defaultCriterionMapping(level string) map[string]string {
	if strings.EqualFold(level, project.EvaluationLevelConversation) {
		return map[string]string{
			"messages":         "{{item.messages}}",
			"tool_definitions": "{{item.tool_definitions}}",
		}
	}
	return map[string]string{
		"query":            "{{item.query}}",
		"response":         "{{item.response}}",
		"tool_calls":       "{{item.tool_calls}}",
		"tool_definitions": "{{item.tool_definitions}}",
	}
}

// planCriterion overlays source-specific and authored bindings on the standard
// defaults. Additional required inputs need explicit mappings; accepted schema
// properties alone are never evidence that grounding or reference data exists.
// Explicit generated columns permit authored simulation bindings, not defaults.
func planCriterion(
	ref evalcore.EvaluatorRef,
	schema *eval_api.EvaluatorSummary,
	targetBindings map[string]string,
	datasetColumns map[string]bool,
	explicitGeneratedColumns map[string]bool,
	level string,
) (*criterionPlan, error) {
	var required []string
	if dataSchema := schema.DataSchema(); dataSchema != nil {
		required = dataSchema.Required
	}
	_, explicitMessages := ref.DataMapping[conversationField]
	_, explicitQuery := ref.DataMapping["query"]
	_, explicitResponse := ref.DataMapping["response"]
	if explicitMessages && (explicitQuery || explicitResponse) {
		return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
			fmt.Sprintf("evaluator %q: dataMapping combines messages with query or response", ref.Evaluator),
			"Map messages for a complete interaction, or query and response separately, but not both.")
	}
	mappingLevel := level
	switch {
	case explicitMessages:
		mappingLevel = project.EvaluationLevelConversation
	case explicitQuery || explicitResponse:
		mappingLevel = project.EvaluationLevelTurn
	}

	plan := &criterionPlan{
		dataMapping: defaultCriterionMapping(mappingLevel),
		initParams:  map[string]any{},
	}
	if explicitGeneratedColumns != nil {
		plan.dataMapping = map[string]string{conversationField: "{{item.messages}}"}
	}

	for field := range plan.dataMapping {
		if binding, ok := targetBindings[field]; ok {
			plan.dataMapping[field] = binding
		}
	}

	// A declared mapping is the author saying the inference got it wrong, so it
	// wins. Anything it binds to an item column is a column the schema has to
	// declare, whether or not inference found it.
	for _, field := range slices.Sorted(maps.Keys(ref.DataMapping)) {
		binding := ref.DataMapping[field]
		if strings.TrimSpace(binding) == "" {
			return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
				fmt.Sprintf("evaluator %q: dataMapping for %q is empty", ref.Evaluator, field),
				"Supply a dataset or sample binding, or remove the entry to use its default.")
		}
		plan.dataMapping[field] = binding
		if column, ok := itemColumn(binding); ok && datasetColumns != nil &&
			!datasetColumns[column] && !explicitGeneratedColumns[column] {
			return nil, messages.EvaluatorNeedsFields(ref.Evaluator, []string{column})
		}
	}
	// Derive columns from the final mapping, not from defaults an override replaced.
	for _, field := range slices.Sorted(maps.Keys(plan.dataMapping)) {
		binding := plan.dataMapping[field]
		if column, ok := itemColumn(binding); ok && !contains(plan.itemFields, column) {
			plan.itemFields = append(plan.itemFields, column)
		}
	}

	var missing []string
	requiredInputs := map[string]bool{}
	for _, field := range required {
		switch {
		case strings.EqualFold(mappingLevel, project.EvaluationLevelConversation) && contains(turnFields, field):
			requiredInputs[conversationField] = true
		case !strings.EqualFold(mappingLevel, project.EvaluationLevelConversation) && field == conversationField:
			requiredInputs["query"], requiredInputs["response"] = true, true
		default:
			requiredInputs[field] = true
		}
	}
	for _, field := range slices.Sorted(maps.Keys(requiredInputs)) {
		binding, bound := plan.dataMapping[field]
		column, fromItem := itemColumn(binding)
		if !bound || (fromItem && datasetColumns != nil && !datasetColumns[column] && !explicitGeneratedColumns[column]) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("evaluator %q requires mapped inputs %s that the selected source does not provide",
				ref.Evaluator, quotedList(missing)),
			"Add dataMapping entries for these inputs that reference actual source columns, "+
				"and include those columns in every dataset row. "+
				"Context and ground_truth are not inferred from the catalog.")
	}

	if !schema.SupportsLevel(level) {
		return nil, messages.EvaluatorLevelUnsupported(
			ref.Evaluator, level, schema.SupportedEvaluationLevels)
	}

	initSchema := schema.InitSchema()
	accepts := func(name string) bool {
		// Only an absent schema falls back to the historical parameters;
		// builtin.ifeval publishes an empty one and takes none.
		if initSchema == nil {
			return name == "deployment_name" || name == "threshold"
		}
		return initSchema.Accepts(name)
	}

	// Evaluators disagree on what the judge model is called: built-ins declare
	// deployment_name, custom rubrics declare model. The declaration names one
	// of them; bind whichever the evaluator actually accepts rather than
	// forwarding a spelling it will reject.
	for name, value := range ref.InitializationParameters {
		if accepts(name) {
			plan.initParams[name] = value
			continue
		}
		if alias, ok := judgeModelAliases[name]; ok && accepts(alias) {
			plan.initParams[alias] = value
		}
	}
	if level != "" && accepts("evaluation_level") {
		plan.initParams["evaluation_level"] = level
	}

	if initSchema != nil {
		var missingInit []string
		for _, name := range initSchema.Required {
			if _, ok := plan.initParams[name]; !ok {
				missingInit = append(missingInit, name)
			}
		}
		if len(missingInit) > 0 {
			return nil, messages.EvaluatorNeedsInitParams(ref.Evaluator, missingInit)
		}
	}

	return plan, nil
}

// checkEvaluatorRequirements refuses a declaration the evaluators cannot
// satisfy, before anything is published.
//
// The same checks happen while building the request, but that runs after the
// datasets and evaluators have been pushed -- so a missing judge deployment
// cost an immutable dataset version per attempt, and the version numbers climb
// whether or not the eval was ever created. Only what the published contract
// alone can settle is checked here: the data mapping needs the dataset's
// columns, which is a separate question.
func checkEvaluatorRequirements(
	eval *project.Eval,
	schemas map[string]*eval_api.EvaluatorSummary,
) error {
	level := resolveLevel(eval)
	for _, ref := range eval.Evaluators {
		schema := schemas[ref.Evaluator]
		if schema == nil {
			// Nothing published to check against. The service still gets the
			// last word, which is what happened before this existed.
			continue
		}
		if !schema.SupportsLevel(level) {
			return messages.EvaluatorLevelUnsupported(
				ref.Evaluator, level, schema.SupportedEvaluationLevels)
		}

		initSchema := schema.InitSchema()
		if initSchema == nil {
			continue
		}
		var missing []string
		for _, name := range initSchema.Required {
			if declaredInitParam(ref, name) {
				continue
			}
			if name == "evaluation_level" && level != "" {
				continue
			}
			missing = append(missing, name)
		}
		if len(missing) > 0 {
			return messages.EvaluatorNeedsInitParams(ref.Evaluator, missing)
		}
	}
	return nil
}

// declaredInitParam reports whether the reference supplies a parameter under
// either spelling of the judge deployment.
func declaredInitParam(ref evalcore.EvaluatorRef, name string) bool {
	if _, ok := ref.InitializationParameters[name]; ok {
		return true
	}
	if alias, ok := judgeModelAliases[name]; ok {
		_, declared := ref.InitializationParameters[alias]
		return declared
	}
	return false
}

// judgeModelAliases maps the two spellings of the judge deployment onto each
// other, so one declaration works whichever the evaluator publishes.
var judgeModelAliases = map[string]string{
	"deployment_name": "model",
	"model":           "deployment_name"}

// itemColumn reads the dataset column out of an `{{item.<name>}}` binding.
func itemColumn(binding string) (string, bool) {
	const prefix, suffix = "{{item.", "}}"
	if !strings.HasPrefix(binding, prefix) || !strings.HasSuffix(binding, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(binding, prefix), suffix)
	if name == "" {
		return "", false
	}
	return name, true
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

// buildEvalRequest converts an eval declaration into the create
// request. Each evaluator becomes a testing criterion bound to its own
// contract, and the item schema declares every dataset column those bindings
// reference.
//
// schemas may be nil or partial; standard defaults do not depend on them.
// datasetColumns may be nil for service-backed sources whose rows are not local.
func buildEvalRequest(
	group *project.Eval,
	schemas map[string]*eval_api.EvaluatorSummary,
	datasetColumns map[string]bool,
) (*eval_api.CreateOpenAIEvalRequest, error) {
	return buildEvalRequestWithAvailableColumns(group, schemas, datasetColumns, datasetColumns)
}

func buildEvalRequestWithAvailableColumns(
	group *project.Eval,
	schemas map[string]*eval_api.EvaluatorSummary,
	datasetColumns map[string]bool,
	availableColumns map[string]bool,
) (*eval_api.CreateOpenAIEvalRequest, error) {
	metadata := map[string]string{}
	hasTarget := group.Target != nil && group.Target.Name != ""
	targetType := ""
	if hasTarget {
		metadata[metaAgent] = group.Target.Name
		targetType = group.Target.Type
		if targetType == "" {
			targetType = project.TargetTypeAgent
		}
	}
	targetBindings := sampleBindingsFor(targetType)
	retrievedResponses := group.Source != nil && group.Source.Type == project.SourceTypeResponses
	traced := group.Source != nil && group.Source.Type == project.SourceTypeTraces
	if retrievedResponses {
		// Retrieved responses expose generated sample output too, even though
		// this run does not invoke a target.
		targetBindings = sampleBindings
	} else if traced {
		// Target names can filter recorded traces; they do not invoke an agent.
		targetBindings = nil
	}

	// A simulation is graded on the conversations the run creates, not on the
	// seed rows it creates them from. The seeds carry test_case_description and
	// simulation_configuration; the graded item carries `messages`. Binding the seed
	// columns here is how a conversation evaluator ended up either refused at
	// deploy for a column the seeds do not have, or created with no binding for
	// the conversation it was meant to score.
	//
	// The sample namespace goes with it: the service holds the conversation
	// itself, so there is no per-row target invocation to produce `sample`.
	simulated := group.Simulation != nil
	var explicitGeneratedColumns map[string]bool
	if simulated {
		datasetColumns = map[string]bool{conversationField: true}
		availableColumns = datasetColumns
		explicitGeneratedColumns = map[string]bool{"tool_definitions": true}
		targetBindings = nil
	}

	metadata[metaEvalName] = group.Name
	// The create request has no description field, so the group's own
	// description rides in metadata rather than being dropped.
	if group.Description != "" {
		metadata[metaDescription] = group.Description
	}

	level := group.EvaluationLevel

	req := &eval_api.CreateOpenAIEvalRequest{
		Name:     group.Name,
		Metadata: metadata,
	}

	itemFields := map[string]bool{}
	itemProperties := map[string]any{}

	for _, ref := range group.Evaluators {
		schema := schemas[evaluatorSchemaKey(ref.Evaluator, ref.Version)]
		if schema == nil {
			schema = schemas[ref.Evaluator]
		}
		if schema == nil {
			schema = &eval_api.EvaluatorSummary{Name: ref.Evaluator}
		}

		bindings := targetBindings
		if !traced {
			if dataSchema := schema.DataSchema(); dataSchema != nil &&
				bindings["response"] == "{{sample.output_items}}" {
				if property, ok := dataSchema.Properties["response"].(map[string]any); ok &&
					property["type"] == "string" {
					bindings = maps.Clone(bindings)
					bindings["response"] = "{{sample.output_text}}"
				}
			}
		}
		plan, err := planCriterion(ref, schema, bindings, datasetColumns, explicitGeneratedColumns, level)
		if err != nil {
			return nil, err
		}
		if group.IsLocalSource() {
			// Required and explicit inputs were checked above. Do not bind absent
			// optional default columns on an explicitly local file.
			plan.itemFields = nil
			for _, field := range slices.Sorted(maps.Keys(plan.dataMapping)) {
				binding := plan.dataMapping[field]
				column, item := itemColumn(binding)
				if !item {
					continue
				}
				if _, explicit := ref.DataMapping[field]; !explicit && !availableColumns[column] {
					delete(plan.dataMapping, field)
					continue
				}
				if !contains(plan.itemFields, column) {
					plan.itemFields = append(plan.itemFields, column)
				}
			}
		}

		criterion := eval_api.TestingCriterion{
			Type: "azure_ai_evaluator",
			// Name labels the criterion in results and defaults to the
			// evaluator without its builtin prefix; EvaluatorName keeps it.
			Name:          ref.CriterionName(),
			EvaluatorName: ref.Evaluator,
			DataMapping:   plan.dataMapping,
		}
		if ref.Version != "" {
			criterion.EvaluatorVersion = ref.Version
		}
		if len(plan.initParams) > 0 {
			criterion.InitializationParameters = plan.initParams
		}
		for _, field := range plan.itemFields {
			itemFields[field] = true
		}
		for _, field := range slices.Sorted(maps.Keys(plan.dataMapping)) {
			binding := plan.dataMapping[field]
			if column, ok := itemColumn(binding); ok {
				property := itemProperty(field)
				if prior, exists := itemProperties[column]; exists && !reflect.DeepEqual(prior, property) {
					itemProperties[column] = map[string]any{"allOf": []any{prior, property}}
				} else {
					itemProperties[column] = property
				}
			}
		}

		req.TestingCriteria = append(req.TestingCriteria, criterion)
	}

	// Declared whether or not a criterion bound it: the conversations a
	// simulation creates are what its rows hold, and a schema that omits the
	// column they arrive in describes a different dataset.
	if simulated {
		itemFields[conversationField] = true
	}

	req.DataSourceConfig = &eval_api.DataSourceConfig{
		Type:                "custom",
		IncludeSampleSchema: (hasTarget || retrievedResponses) && !simulated && !traced,
		ItemSchema:          itemSchema(itemFields),
	}
	properties := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	maps.Copy(properties, itemProperties)
	if simulated {
		req.DataSourceConfig.ItemSchema["required"] = []string{conversationField}
	}
	if isResponsesEval(group) {
		req.DataSourceConfig = &eval_api.DataSourceConfig{
			Type: "azure_ai_source", Scenario: "responses",
		}
	}
	if group.IsLocalSource() {
		req.DataSourceConfig.ItemSchema = localItemSchema(group, req.TestingCriteria, schemas)
	}

	return req, nil
}

// itemSchema declares the dataset columns the criteria bind to. It always
// declares at least `query`, the column an agent target reads.
func itemSchema(fields map[string]bool) map[string]any {
	if len(fields) == 0 {
		fields = map[string]bool{"query": true}
	}
	properties := map[string]any{}
	for field := range fields {
		properties[field] = itemProperty(field)
	}
	return map[string]any{
		"type":       "object",
		"properties": properties,
	}
}

// itemProperty describes the standard interaction formats, including tool
// results needed for groundedness. Aliased columns use the evaluator field's
// shape, not the spelling the dataset author chose for the column.
func itemProperty(field string) map[string]any {
	messages := map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	switch field {
	case conversationField, "tool_calls":
		return messages
	case "query", "response":
		return map[string]any{"anyOf": []any{map[string]any{"type": "string"}, messages}}
	case "tool_definitions":
		return map[string]any{"anyOf": []any{
			map[string]any{"type": "string"}, map[string]any{"type": "object"}, messages,
		}}
	default:
		return map[string]any{"type": "string"}
	}
}
