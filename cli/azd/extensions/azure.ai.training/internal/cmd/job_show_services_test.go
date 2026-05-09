// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransformServiceInstanceResponse_EmptyCases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "nil/empty raw", raw: ""},
		{name: "missing instances key", raw: `{}`},
		{name: "null instances", raw: `{"instances":null}`},
		{name: "empty instances map", raw: `{"instances":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, empty, err := transformServiceInstanceResponse(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !empty {
				t.Fatalf("expected empty=true, got out=%s", string(out))
			}
		})
	}
}

func TestTransformServiceInstanceResponse_InvalidJSON(t *testing.T) {
	_, _, err := transformServiceInstanceResponse(json.RawMessage(`{not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestTransformServiceInstanceResponse_FullSample exercises the AML-parity
// transformations against a representative response: envelope strip, error
// flatten (string + null), all 6 fields always present (null for unset),
// snake_case keys, alphabetical service ordering, and no HTML escaping
// of <, >, & in endpoint URLs.
func TestTransformServiceInstanceResponse_FullSample(t *testing.T) {
	input := `{
  "instances": {
    "my_ssh": {
      "type": "SSH",
      "port": 8705,
      "status": "Running",
      "error": null,
      "endpoint": "",
      "properties": {"ProxyEndpoint": "wss://ssh-host"}
    },
    "tensorboard": {
      "type": "TensorBoard",
      "port": 6006,
      "status": "Failed",
      "error": {
        "error": {
          "code": null,
          "message": "failed to start endpoint tensorboard"
        },
        "time": "0001-01-01T00:00:00+00:00"
      },
      "endpoint": "https://tnsrb-host",
      "properties": {}
    },
    "vscode": {
      "type": "VSCode",
      "status": "Running",
      "endpoint": "vscode://x?a=1&b=2",
      "properties": {"ProxyEndpoint": "https://<port>-host"}
    },
    "grafana": {
      "type": "Grafana",
      "port": 3000,
      "status": "Running",
      "error": null,
      "endpoint": "https://3000-host/<path>",
      "properties": {}
    }
  }
}`

	out, empty, err := transformServiceInstanceResponse(json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if empty {
		t.Fatal("expected non-empty output")
	}

	// 1. No top-level instances envelope.
	if strings.Contains(string(out), `"instances"`) {
		t.Errorf("output should not contain 'instances' envelope: %s", out)
	}

	// 2. Decode and inspect each service.
	var got map[string]map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("failed to decode output: %v", err)
	}

	// 3. Service ordering — Go marshals map keys sorted alphabetically.
	wantOrder := []string{`"grafana"`, `"my_ssh"`, `"tensorboard"`, `"vscode"`}
	prev := -1
	for _, key := range wantOrder {
		idx := strings.Index(string(out), key)
		if idx < 0 {
			t.Fatalf("expected key %s in output: %s", key, out)
		}
		if idx <= prev {
			t.Errorf("services not sorted alphabetically: %s appears at idx %d (previous %d)", key, idx, prev)
		}
		prev = idx
	}

	// 4. All 6 fields per service, in order; missing values should be `null`.
	requiredKeys := []string{"type", "port", "status", "error", "endpoint", "properties"}
	for svcName, svc := range got {
		for _, k := range requiredKeys {
			v, ok := svc[k]
			if !ok {
				t.Errorf("service %q missing required field %q", svcName, k)
				continue
			}
			if len(v) == 0 {
				t.Errorf("service %q field %q is empty raw", svcName, k)
			}
		}
		if len(svc) != len(requiredKeys) {
			t.Errorf("service %q has %d fields, want %d (extra: %v)", svcName, len(svc), len(requiredKeys), svc)
		}
	}

	// 5. vscode.port should be the literal `null` (input omitted it).
	if got["vscode"] == nil {
		t.Fatal("missing vscode entry")
	}
	if string(got["vscode"]["port"]) != "null" {
		t.Errorf("vscode.port = %s, want null", got["vscode"]["port"])
	}
	// vscode.error should also be null (input omitted it).
	if string(got["vscode"]["error"]) != "null" {
		t.Errorf("vscode.error = %s, want null", got["vscode"]["error"])
	}

	// 6. tensorboard.error flattened to the message string.
	wantErr := `"failed to start endpoint tensorboard"`
	if string(got["tensorboard"]["error"]) != wantErr {
		t.Errorf("tensorboard.error = %s, want %s", got["tensorboard"]["error"], wantErr)
	}

	// 7. my_ssh.error explicit null preserved as null.
	if string(got["my_ssh"]["error"]) != "null" {
		t.Errorf("my_ssh.error = %s, want null", got["my_ssh"]["error"])
	}

	// 8. No HTML-escaping of <, >, & in URLs.
	for _, bad := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(string(out), bad) {
			t.Errorf("output contains HTML-escape sequence %q (should be literal): %s", bad, out)
		}
	}
	// Positive: literal & must be present in vscode endpoint.
	if !strings.Contains(string(out), `"vscode://x?a=1&b=2"`) {
		t.Errorf("vscode endpoint not preserved verbatim: %s", out)
	}
}

func TestFlattenServiceError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "nil raw", in: "", want: "null"},
		{name: "literal null", in: "null", want: "null"},
		{name: "valid envelope with message", in: `{"error":{"message":"boom"},"time":"0001-01-01T00:00:00+00:00"}`, want: `"boom"`},
		{name: "envelope with empty message", in: `{"error":{"message":""}}`, want: "null"},
		{name: "missing inner error", in: `{"time":"x"}`, want: "null"},
		{name: "inner error null", in: `{"error":null}`, want: "null"},
		{name: "malformed json", in: `{not json`, want: "null"},
		{name: "message with html chars not escaped", in: `{"error":{"message":"a < b & c > d"}}`, want: `"a < b & c > d"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(flattenServiceError(json.RawMessage(tt.in)))
			if got != tt.want {
				t.Errorf("flattenServiceError(%s) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestMarshalNoEscape(t *testing.T) {
	in := map[string]string{"u": "a<b>c&d"}
	out, err := marshalNoEscape(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := string(out)
	if strings.Contains(got, `\u003c`) || strings.Contains(got, `\u003e`) || strings.Contains(got, `\u0026`) {
		t.Errorf("output contains HTML escapes: %s", got)
	}
	if !strings.Contains(got, `"a<b>c&d"`) {
		t.Errorf("output missing literal value: %s", got)
	}
	// Must not have a trailing newline.
	if strings.HasSuffix(got, "\n") {
		t.Errorf("output should not end with newline: %q", got)
	}
}
