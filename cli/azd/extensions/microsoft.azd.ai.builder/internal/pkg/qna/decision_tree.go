package qna

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// DecisionTree represents the entire decision tree structure.
type DecisionTree struct {
	azdClient *azdext.AzdClient
	config    DecisionTreeConfig
}

type DecisionTreeConfig struct {
	Questions map[string]Question `json:"questions"`
	End       EndNode             `json:"end"`
}

func NewDecisionTree(azdClient *azdext.AzdClient, config DecisionTreeConfig) *DecisionTree {
	return &DecisionTree{
		azdClient: azdClient,
		config:    config,
	}
}

// Question represents a single prompt in the decision tree.
type Question struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Type     QuestionType   `json:"type"`
	Choices  []Choice       `json:"choices,omitempty"`
	Branches map[any]string `json:"branches"`
	Binding  any            `json:"-"`
}

type Choice struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// EndNode represents the terminal state of the decision tree.
type EndNode struct {
	Message string `json:"message"`
}

// QuestionType defines the allowed input types.
type QuestionType string

const (
	TextInput    QuestionType = "text"
	BooleanInput QuestionType = "boolean"
	SingleSelect QuestionType = "single_select"
	MultiSelect  QuestionType = "multi_select"
)

func (t *DecisionTree) Run(ctx context.Context) error {
	rootQuestion, has := t.config.Questions["root"]
	if !has {
		return errors.New("root question not found")
	}

	return t.askQuestion(ctx, rootQuestion)
}

func (t *DecisionTree) askQuestion(ctx context.Context, question Question) error {
	var err error
	var value any

	switch question.Type {
	case TextInput:
		value, err = t.askTextQuestion(ctx, question)
	case BooleanInput:
		value, err = t.askBooleanQuestion(ctx, question)
	case SingleSelect:
		value, err = t.askSingleSelectQuestion(ctx, question)
	case MultiSelect:
		value, err = t.askMultiSelectQuestion(ctx, question)
	default:
		return errors.New("unsupported question type")
	}

	if err != nil {
		return fmt.Errorf("failed to ask question: %w", err)
	}

	var nextQuestionKey string

	// No branches means no further questions
	if len(question.Branches) == 0 {
		return nil
	}

	// Handle the case where the branch is a wildcard
	if branchValue, has := question.Branches["*"]; has {
		nextQuestionKey = branchValue
	} else {
		// Handle the case where the branch is based on the user's response
		switch v := value.(type) {
		case string:
			if branch, has := question.Branches[v]; has {
				nextQuestionKey = branch
			}
		case bool:
			if branch, has := question.Branches[v]; has {
				nextQuestionKey = branch
			}
		case []string:
			for _, selectedValue := range v {
				if branch, has := question.Branches[selectedValue]; has {
					nextQuestionKey = branch
					break
				}
			}
		default:
			return errors.New("unsupported value type")
		}
	}

	if nextQuestion, has := t.config.Questions[nextQuestionKey]; has {
		return t.askQuestion(ctx, nextQuestion)
	}

	if nextQuestionKey == "end" {
		return nil
	}

	return fmt.Errorf("next question not found: %s", nextQuestionKey)
}

func (t *DecisionTree) askTextQuestion(ctx context.Context, question Question) (any, error) {
	promptResponse, err := t.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  question.Text,
			Required: true,
		},
	})
	if err != nil {
		return nil, err
	}

	if stringPtr, ok := question.Binding.(*string); ok {
		*stringPtr = promptResponse.Value
	}

	return promptResponse.Value, nil
}

func (t *DecisionTree) askBooleanQuestion(ctx context.Context, question Question) (any, error) {
	defaultValue := true
	confirmResponse, err := t.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      question.Text,
			DefaultValue: &defaultValue,
		},
	})
	if err != nil {
		return nil, err
	}

	if boolPtr, ok := question.Binding.(*bool); ok {
		*boolPtr = *confirmResponse.Value
	}

	return confirmResponse.Value, nil
}

func (t *DecisionTree) askSingleSelectQuestion(ctx context.Context, question Question) (any, error) {
	choices := make([]*azdext.SelectChoice, len(question.Choices))
	for i, choice := range question.Choices {
		choices[i] = &azdext.SelectChoice{
			Label: choice.Label,
			Value: choice.Value,
		}
	}

	selectResponse, err := t.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: question.Text,
			Choices: choices,
		},
	})
	if err != nil {
		return nil, err
	}

	selectedChoice := question.Choices[*selectResponse.Value]

	if stringPtr, ok := question.Binding.(*string); ok {
		*stringPtr = selectedChoice.Value
	}

	return selectedChoice.Value, nil
}

func (t *DecisionTree) askMultiSelectQuestion(ctx context.Context, question Question) (any, error) {
	choices := make([]*azdext.MultiSelectChoice, len(question.Choices))
	for i, choice := range question.Choices {
		choices[i] = &azdext.MultiSelectChoice{
			Label: choice.Label,
			Value: choice.Value,
		}
	}

	multiSelectResponse, err := t.azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: question.Text,
			Choices: choices,
		},
	})
	if err != nil {
		return nil, err
	}

	selectedChoices := make([]string, len(multiSelectResponse.Values))
	for i, value := range multiSelectResponse.Values {
		selectedChoices[i] = value.Value
	}

	if stringSlicePtr, ok := question.Binding.(*[]string); ok {
		*stringSlicePtr = selectedChoices
	}

	return selectedChoices, nil
}
