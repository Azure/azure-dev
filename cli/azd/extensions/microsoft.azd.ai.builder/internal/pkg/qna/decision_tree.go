// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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

type Prompt interface {
	Ask(ctx context.Context, question Question) (any, error)
}

// Question represents a single prompt in the decision tree.
type Question struct {
	ID        string         `json:"id"`
	Branches  map[any]string `json:"branches"`
	Next      string         `json:"next"`
	Binding   any            `json:"-"`
	Prompt    Prompt         `json:"prompt,omitempty"`
	BeforeAsk func(ctx context.Context, question *Question) error
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
	if question.Prompt == nil {
		return errors.New("question prompt is nil")
	}

	if question.BeforeAsk != nil {
		if err := question.BeforeAsk(ctx, &question); err != nil {
			return fmt.Errorf("before ask function failed: %w", err)
		}
	}

	value, err := question.Prompt.Ask(ctx, question)
	if err != nil {
		return fmt.Errorf("failed to ask question: %w", err)
	}

	var nextQuestionKey string

	// Handle the case where the branch is based on the user's response
	if len(question.Branches) > 0 {
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
			// Handle multi-select case
			for _, selectedValue := range v {
				branch, has := question.Branches[selectedValue]
				if !has {
					return fmt.Errorf("branch not found for selected value: %s", selectedValue)
				}

				question, has := t.config.Questions[branch]
				if !has {
					return fmt.Errorf("question not found for branch: %s", branch)
				}

				if err = t.askQuestion(ctx, question); err != nil {
					return fmt.Errorf("failed to ask question: %w", err)
				}
			}
		default:
			return errors.New("unsupported value type")
		}
	}

	if nextQuestionKey == "" {
		nextQuestionKey = question.Next
	}

	if nextQuestionKey == "" || nextQuestionKey == "end" {
		return nil
	}

	nextQuestion, has := t.config.Questions[nextQuestionKey]
	if !has {
		return fmt.Errorf("next question not found: %s", nextQuestionKey)
	}

	return t.askQuestion(ctx, nextQuestion)
}
