// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON preserves numeric values in stored inline rows for exact-value reruns.
func (s *EvalRunDataContent) UnmarshalJSON(data []byte) error {
	type content EvalRunDataContent
	var decoded content
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*s = EvalRunDataContent(decoded)
	return nil
}
