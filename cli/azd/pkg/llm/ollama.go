// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"log"
	"os"

	"github.com/tmc/langchaingo/llms/ollama"
)

func loadOllama() (InfoResponse, error) {
	defaultLlamaVersion := "llama3"

	if value, isDefined := os.LookupEnv("AZD_OLLAMA_MODEL"); isDefined {
		log.Printf("Found AZD_OLLAMA_MODEL with %s. Using this model", value)
		defaultLlamaVersion = value
	}

	_, err := ollama.New(
		ollama.WithModel(defaultLlamaVersion),
	)
	if err != nil {
		return InfoResponse{}, err
	}

	return InfoResponse{
		Type:    LlmTypeOllama,
		IsLocal: true,
		Model: LlmModel{
			Name:    defaultLlamaVersion,
			Version: "latest",
		},
	}, nil
}
