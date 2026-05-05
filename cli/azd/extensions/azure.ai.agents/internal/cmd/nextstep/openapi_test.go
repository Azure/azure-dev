// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSpec(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "openapi-x-local.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return p
}

func TestCachedOpenAPISpecPath(t *testing.T) {
	got := CachedOpenAPISpecPath(filepath.Join("a", "b"), "my/agent..name", "local")
	want := filepath.Join("a", "b", "openapi-my_agent_name-local.json")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestExtractInvokeExample_DirectExample(t *testing.T) {
	spec := `{
	  "paths": {
	    "/invocations": {
	      "post": {
	        "requestBody": {
	          "content": {
	            "application/json": {
	              "example": {"prompt": "Hi"}
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	p := writeSpec(t, spec)
	got, err := ExtractInvokeExample(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"prompt":"Hi"}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractInvokeExample_ExamplesMap(t *testing.T) {
	spec := `{
	  "paths": {
	    "/invocations": {
	      "post": {
	        "requestBody": {
	          "content": {
	            "application/json": {
	              "examples": {
	                "default": {"value": {"prompt": "Greet"}}
	              }
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	p := writeSpec(t, spec)
	got, _ := ExtractInvokeExample(p)
	if got != `{"prompt":"Greet"}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractInvokeExample_SchemaExample(t *testing.T) {
	spec := `{
	  "paths": {
	    "/invocations": {
	      "post": {
	        "requestBody": {
	          "content": {
	            "application/json": {
	              "schema": {"example": {"prompt": "Schema"}}
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	p := writeSpec(t, spec)
	got, _ := ExtractInvokeExample(p)
	if got != `{"prompt":"Schema"}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractInvokeExample_SynthesizeFromSchema(t *testing.T) {
	spec := `{
	  "paths": {
	    "/invocations": {
	      "post": {
	        "requestBody": {
	          "content": {
	            "application/json": {
	              "schema": {
	                "type": "object",
	                "properties": {
	                  "prompt": {"type": "string", "example": "Hello"},
	                  "max_tokens": {"type": "integer", "default": 100}
	                }
	              }
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	p := writeSpec(t, spec)
	got, err := ExtractInvokeExample(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Order in JSON encoding of map[string]any is deterministic in encoding/json (sorted by key).
	want := `{"max_tokens":100,"prompt":"Hello"}`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestExtractInvokeExample_NoSpec(t *testing.T) {
	got, err := ExtractInvokeExample(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractInvokeExample_EmptySpec(t *testing.T) {
	got, _ := ExtractInvokeExample(writeSpec(t, `{}`))
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractInvokeExample_BadJSON(t *testing.T) {
	_, err := ExtractInvokeExample(writeSpec(t, `not json`))
	if err == nil {
		t.Errorf("expected parse error")
	}
}

func TestExtractInvokeExample_NoApplicationJSONFallback(t *testing.T) {
	spec := `{
	  "paths": {
	    "/invocations": {
	      "post": {
	        "requestBody": {
	          "content": {
	            "text/plain": {"example": "Howdy"}
	          }
	        }
	      }
	    }
	  }
	}`
	got, _ := ExtractInvokeExample(writeSpec(t, spec))
	if got != `"Howdy"` {
		t.Errorf("got %q", got)
	}
}
