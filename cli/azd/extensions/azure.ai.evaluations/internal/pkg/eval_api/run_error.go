// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"encoding/json"
	"strings"
)

// maxErrorDepth bounds how far a nested error is followed. Real error bodies
// nest a level or two; a limit keeps a hostile or cyclic-looking payload cheap.
const maxErrorDepth = 4

// errorMembers are the members of an error object that can hold its reason.
//
// Decoding through tagged fields, rather than looking keys up in a map, keeps
// encoding/json's case-insensitive member matching, so `Message` and
// `innerError` read the same as `message` and `innererror`.
type errorMembers struct {
	Code    json.RawMessage `json:"code"`
	Message json.RawMessage `json:"message"`
	// The members below carry a further explanation when the message is empty:
	// the Azure error contract's `details` array and `innererror`, its
	// snake_case spelling, and an OpenAI-style `error` wrapper.
	Details    json.RawMessage `json:"details"`
	InnerError json.RawMessage `json:"innererror"`
	InnerSnake json.RawMessage `json:"inner_error"`
	Error      json.RawMessage `json:"error"`
}

func (m errorMembers) nested() []json.RawMessage {
	return []json.RawMessage{m.Details, m.InnerError, m.InnerSnake, m.Error}
}

// UnmarshalJSON reads an error however the service shaped it.
//
// A failed run or job carries its reason in an error object, and decoding used
// to demand exactly {"code": string, "message": string}. An error sent as a
// bare string, or with its message under `details` or `innererror`, either
// failed the whole response -- hiding the run -- or left the human views with
// nothing to print but the word "failed". Anything that cannot be read as text
// is ignored rather than rejected, because losing the reason is better than
// losing the run that carries it.
//
// Text is kept as the service sent it; Reason trims. That keeps an error that
// already decoded under the old contract emitting the same bytes under -o json.
func (e *JobError) UnmarshalJSON(data []byte) error {
	*e = JobError{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '{' {
		e.Message = errorText(trimmed, 0)
		return nil
	}

	var members errorMembers
	if err := json.Unmarshal(trimmed, &members); err != nil {
		return err
	}
	e.Code = codeText(members.Code)
	e.Message = errorText(members.Message, 0)
	if !blank(e.Message) {
		return nil
	}
	for _, nested := range members.nested() {
		if text := errorText(nested, 0); !blank(text) {
			e.detail = text
			break
		}
	}
	return nil
}

// Reason is the most specific explanation the error carries: its own message,
// otherwise the first nested one. Empty when the service gave none.
func (e *JobError) Reason() string {
	if e == nil {
		return ""
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		return message
	}
	return strings.TrimSpace(e.detail)
}

func blank(text string) bool {
	return strings.TrimSpace(text) == ""
}

// codeText reads an error code, which is a string. A numeric or structured code
// is left to the service's own JSON rather than re-typed here.
func codeText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	return errorText(raw, 0)
}

// errorText finds readable text in one JSON value: a string is itself, an
// object is its message (else its nested explanation), an array is its
// non-blank entries joined, and numbers are their literal text.
func errorText(raw json.RawMessage, depth int) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || depth > maxErrorDepth {
		return ""
	}
	switch raw[0] {
	case '"':
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
		return text
	case '{':
		var members errorMembers
		if json.Unmarshal(raw, &members) != nil {
			return ""
		}
		if text := errorText(members.Message, depth+1); !blank(text) {
			return text
		}
		for _, nested := range members.nested() {
			if text := errorText(nested, depth+1); !blank(text) {
				return text
			}
		}
		return ""
	case '[':
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) != nil {
			return ""
		}
		var texts []string
		for _, entry := range entries {
			if text := errorText(entry, depth+1); !blank(text) {
				texts = append(texts, strings.TrimSpace(text))
			}
		}
		return strings.Join(texts, "; ")
	case 't', 'f', 'n':
		return ""
	default:
		return string(raw)
	}
}
