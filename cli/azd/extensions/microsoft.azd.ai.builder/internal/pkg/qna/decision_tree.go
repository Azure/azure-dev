// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package qna

import (
	"context"
	"errors"
	"fmt"
	"log"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// DecisionTree represents the entire decision tree structure.
type DecisionTree struct {
	questions map[string]Question
}

func NewDecisionTree(questions map[string]Question) *DecisionTree {
	return &DecisionTree{
		questions: questions,
	}
}

type Prompt interface {
	Ask(ctx context.Context, question Question) (any, error)
}

// Question represents a single prompt in the decision tree.
type Question struct {
	Branches  map[any][]QuestionReference `json:"branches"`
	Next      []QuestionReference         `json:"next"`
	Binding   any                         `json:"-"`
	Heading   string                      `json:"heading,omitempty"`
	Help      string                      `json:"help,omitempty"`
	Message   string
	Prompt    Prompt `json:"prompt,omitempty"`
	State     map[string]any
	BeforeAsk func(ctx context.Context, question *Question, value any) error
	AfterAsk  func(ctx context.Context, question *Question, value any) error
}

type QuestionReference struct {
	Key   string `json:"id"`
	State map[string]any
}
type Choice struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// QuestionType defines the allowed input types.
type QuestionType string

const (
	TextInput    QuestionType = "text"
	BooleanInput QuestionType = "boolean"
	SingleSelect QuestionType = "single_select"
	MultiSelect  QuestionType = "multi_select"
)

// Run iterates through the decision tree and asks questions based on the user's responses.
// It starts from the root question and continues until it reaches an end node.
func (t *DecisionTree) Run(ctx context.Context) error {
	rootQuestion, has := t.questions["root"]
	if !has {
		return errors.New("root question not found")
	}

	return t.askQuestion(ctx, rootQuestion, nil)
}

// askQuestion recursively asks questions based on the decision tree structure.
func (t *DecisionTree) askQuestion(ctx context.Context, question Question, value any) error {
	if question.State == nil {
		question.State = map[string]any{}
	}

	if question.BeforeAsk != nil {
		if err := question.BeforeAsk(ctx, &question, value); err != nil {
			return fmt.Errorf("before ask function failed: %w", err)
		}
	}

	if question.Heading != "" {
		fmt.Println()
		fmt.Println(output.WithHintFormat(question.Heading))
	}

	if question.Message != "" {
		fmt.Println(question.Message)
		fmt.Println()
	}

	var response any
	var err error

	if question.Prompt != nil {
		response, err = question.Prompt.Ask(ctx, question)
		if err != nil {
			return fmt.Errorf("failed to ask question: %w", err)
		}
	}

	if question.AfterAsk != nil {
		if err := question.AfterAsk(ctx, &question, response); err != nil {
			return fmt.Errorf("after ask function failed: %w", err)
		}
	}

	t.applyBinding(question, response)

	// Handle the case where the branch is based on the user's response
	if len(question.Branches) > 0 {
		selectionValues := []any{}

		switch result := response.(type) {
		case string:
			selectionValues = append(selectionValues, result)
		case bool:
			selectionValues = append(selectionValues, result)
		case []string:
			for _, selectedValue := range result {
				selectionValues = append(selectionValues, selectedValue)
			}
		default:
			return errors.New("unsupported value type")
		}

		// We need to process all the question branches from the selected values
		// Iterate through the selected values and find the corresponding branches
		for _, selectedValue := range selectionValues {
			steps, has := question.Branches[selectedValue]
			if !has {
				log.Printf("branch not found for selected value: %s\n", selectedValue)
				continue
			}

			// Iterate through the steps in the branch
			for _, questionReference := range steps {
				nextQuestion, has := t.questions[questionReference.Key]
				if !has {
					return fmt.Errorf("question not found for branch: %s\n", selectedValue)
				}

				nextQuestion.State = question.State
				if err = t.askQuestion(ctx, nextQuestion, selectedValue); err != nil {
					return fmt.Errorf("failed to ask question: %w", err)
				}

				if err := mergo.Merge(&question.State, nextQuestion.State, mergo.WithOverride); err != nil {
					return fmt.Errorf("failed to merge question states: %w", err)
				}
			}
		}
	}

	// After processing branches, we need to check if there is a next question
	if len(question.Next) == 0 {
		return nil
	}

	for _, nextQuestionRef := range question.Next {
		nextQuestion, has := t.questions[nextQuestionRef.Key]
		if !has {
			return fmt.Errorf("next question not found: %s", nextQuestionRef.Key)
		}

		nextQuestion.State = question.State
		if nextQuestionRef.State != nil {
			if err := mergo.Merge(&nextQuestion.State, nextQuestionRef.State, mergo.WithOverride); err != nil {
				return fmt.Errorf("failed to merge question states: %w", err)
			}
		}

		if err = t.askQuestion(ctx, nextQuestion, response); err != nil {
			return fmt.Errorf("failed to ask next question: %w", err)
		}

		if err := mergo.Merge(&question.State, nextQuestion.State, mergo.WithOverride); err != nil {
			return fmt.Errorf("failed to merge question states: %w", err)
		}
	}

	return nil
}

// applyBinding applies the value to the binding if it exists.
func (t *DecisionTree) applyBinding(question Question, value any) {
	if question.Binding == nil {
		return
	}

	switch binding := question.Binding.(type) {
	case *bool:
		if boolValue, ok := value.(bool); ok {
			*binding = boolValue
		}
	case *int:
		if intValue, ok := value.(int); ok {
			*binding = intValue
		}
	case *float64:
		if floatValue, ok := value.(float64); ok {
			*binding = floatValue
		}
	case *int64:
		if int64Value, ok := value.(int64); ok {
			*binding = int64Value
		}
	case *float32:
		if float32Value, ok := value.(float32); ok {
			*binding = float32Value
		}
	case *int32:
		if int32Value, ok := value.(int32); ok {
			*binding = int32Value
		}
	case *string:
		if strValue, ok := value.(string); ok {
			*binding = strValue
		}
	case *[]string:
		if strSliceValue, ok := value.([]string); ok {
			*binding = append(*binding, strSliceValue...)
		}
		if strValue, ok := value.(string); ok {
			*binding = append(*binding, strValue)
		}
	default:
		log.Printf("unsupported binding type: %T\n", binding)
	}
}
