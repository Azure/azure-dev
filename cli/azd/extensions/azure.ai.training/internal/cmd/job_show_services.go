// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobShowServicesCommand() *cobra.Command {
	var name string
	var nodeIndex int

	cmd := &cobra.Command{
		Use:   "show-services",
		Short: "Show services of a training job per node (e.g. SSH, JupyterLab, TensorBoard)",
		Long: "Show the services running on a specific node of a training job. Output is JSON.\n\n" +
			"Example:\n" +
			"  azd ai training job show-services --name my-job --node-index 0",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if nodeIndex < 0 {
				return fmt.Errorf("--node-index must be >= 0")
			}

			apiClient, err := buildJobAPIClient(ctx)
			if err != nil {
				return err
			}

			// Ensure the job exists before querying its service instances.
			if _, err := apiClient.GetJob(ctx, name); err != nil {
				return fmt.Errorf("failed to get job %q: %w", name, err)
			}

			raw, err := apiClient.GetServiceInstanceRaw(ctx, name, nodeIndex)
			if err != nil {
				return fmt.Errorf("failed to get services for node %d of job %q: %w", nodeIndex, name, err)
			}

			out, empty, err := transformServiceInstanceResponse(raw)
			if err != nil {
				return fmt.Errorf("failed to parse services response: %w", err)
			}
			if empty {
				return fmt.Errorf("no services found for node %d of job %q", nodeIndex, name)
			}

			// Pretty-print the transformed payload (parity with AML CLI shape).
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, out, "", "  "); err != nil {
				fmt.Println(string(out))
				return nil
			}
			fmt.Println(pretty.String())
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name (required)")
	cmd.Flags().IntVar(&nodeIndex, "node-index", 0,
		"Zero-based index of the node in a multi-node job (default 0)")

	return cmd
}

// outputService is the per-service shape we emit. Field order here is the
// JSON key order in the output. All fields are RawMessage so we can default
// missing ones to literal `null` rather than relying on omitempty.
type outputService struct {
	Type       json.RawMessage `json:"type"`
	Port       json.RawMessage `json:"port"`
	Status     json.RawMessage `json:"status"`
	Error      json.RawMessage `json:"error"`
	Endpoint   json.RawMessage `json:"endpoint"`
	Properties json.RawMessage `json:"properties"`
}

// transformServiceInstanceResponse reshapes the AML history serviceinstances
// response to match the AML CLI output:
//   - drop the top-level `instances` envelope
//   - flatten `error` to just the inner message string (else null)
//   - guarantee all 6 fields are present per service (null for unset)
//   - sort service-name keys alphabetically (Go marshals map keys sorted)
//
// Returns (out, empty, err). `empty` is true when the response had no services.
func transformServiceInstanceResponse(raw json.RawMessage) ([]byte, bool, error) {
	if len(raw) == 0 {
		return nil, true, nil
	}

	var envelope struct {
		Instances map[string]json.RawMessage `json:"instances"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false, err
	}
	if len(envelope.Instances) == 0 {
		return nil, true, nil
	}

	null := json.RawMessage("null")
	transformed := make(map[string]outputService, len(envelope.Instances))
	for svcName, svcRaw := range envelope.Instances {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(svcRaw, &fields); err != nil {
			return nil, false, fmt.Errorf("service %q has unexpected shape: %w", svcName, err)
		}
		pick := func(key string) json.RawMessage {
			if v, ok := fields[key]; ok && len(v) > 0 {
				return v
			}
			return null
		}
		transformed[svcName] = outputService{
			Type:       pick("type"),
			Port:       pick("port"),
			Status:     pick("status"),
			Error:      flattenServiceError(fields["error"]),
			Endpoint:   pick("endpoint"),
			Properties: pick("properties"),
		}
	}

	out, err := marshalNoEscape(transformed)
	if err != nil {
		return nil, false, err
	}
	return out, false, nil
}

// marshalNoEscape is like json.Marshal but does not HTML-escape <, >, & in
// strings, so endpoint URLs containing literal angle brackets render cleanly.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder appends a trailing newline; trim it so json.Indent output is clean.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// flattenServiceError extracts the inner error message from the AML error
// envelope. Returns a JSON string literal of the message, or `null` if no
// usable message is present.
//
// Input shape (when set):
//
//	{ "error": { "message": "...", ... }, "time": "...", ... }
//
// Output: "..."  (or null)
func flattenServiceError(raw json.RawMessage) json.RawMessage {
	null := json.RawMessage("null")
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return null
	}

	var envelope struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return null
	}
	if envelope.Error == nil || envelope.Error.Message == "" {
		return null
	}
	encoded, err := marshalNoEscape(envelope.Error.Message)
	if err != nil {
		return null
	}
	return encoded
}
