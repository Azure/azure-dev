// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"encoding/json"
	"strings"

	"azureaieval/internal/failuretext"
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
	// Target names what a detail is about, such as the request member that was
	// rejected. Only a detail entry carries one that matters.
	Target json.RawMessage `json:"target"`
	// The members below carry a further explanation when the message is empty:
	// the Azure error contract's `details` array and `innererror`, its
	// snake_case spelling, and an OpenAI-style `error` wrapper.
	Details    json.RawMessage `json:"details"`
	InnerError json.RawMessage `json:"innererror"`
	InnerSnake json.RawMessage `json:"inner_error"`
	Error      json.RawMessage `json:"error"`
}

// maxCollectedDetails bounds how many entries of a details array are kept. The
// human views print far fewer; the cap only keeps a hostile payload cheap, and
// the entries beyond it are counted so a view can still say how many it left out.
const maxCollectedDetails = 50

// ErrorDetail is one entry of an error's details array: the specific thing the
// service found wrong, which the error's own message often only summarizes.
type ErrorDetail = failuretext.Detail

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
	e.details, e.omittedDetails = readDetails(members.Details)
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

// Diagnostic returns the best text available for a failed job or run,
// falling back to its service code when it carried no explanatory message.
func (e *JobError) Diagnostic() string {
	if reason := e.Reason(); reason != "" {
		return reason
	}
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Code)
}

func blank(text string) bool {
	return strings.TrimSpace(text) == ""
}

// Details are the entries of the error's details array that say something: a
// message, or failing that a code. An entry that is a bare string is a message.
//
// They are read for display only. They are not part of the wire shape this type
// re-emits, so -o json keeps the service's own object.
func (e *JobError) Details() []ErrorDetail {
	if e == nil || len(e.details) == 0 {
		return nil
	}
	return append([]ErrorDetail(nil), e.details...)
}

// OmittedDetails is how many further entries the details array held beyond
// those Details returns.
func (e *JobError) OmittedDetails() int {
	if e == nil {
		return 0
	}
	return e.omittedDetails
}

func readDetails(raw json.RawMessage) ([]ErrorDetail, int) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, 0
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil, 0
	}
	var details []ErrorDetail
	omitted := 0
	// Repeats are dropped before the cap is applied, so a run of identical entries
	// cannot fill the room and hide the one that names the cause.
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = bytes.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}
		var detail ErrorDetail
		if entry[0] == '{' {
			var members errorMembers
			if json.Unmarshal(entry, &members) != nil {
				continue
			}
			detail.Message = strings.TrimSpace(errorText(entry, 0))
			detail.Code = strings.TrimSpace(codeText(members.Code))
			detail.Target = strings.TrimSpace(codeText(members.Target))
		} else {
			detail.Message = strings.TrimSpace(errorText(entry, 0))
		}
		if detail.Message == "" && detail.Code == "" {
			continue
		}
		key := failuretext.Key(failuretext.Detail{Code: detail.Code, Message: detail.Message, Target: detail.Target})
		if key != "" && seen[key] {
			continue
		}
		seen[key] = true
		if len(details) == maxCollectedDetails {
			omitted++
			continue
		}
		details = append(details, detail)
	}
	return details, omitted
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
