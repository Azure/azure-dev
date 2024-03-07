// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type writeCapturer struct {
	writes []string
}

func (lc *writeCapturer) Write(p []byte) (int, error) {
	lc.writes = append(lc.writes, string(p))
	return len(p), nil
}

func Test_lineWriter_Write(t *testing.T) {
	tests := []struct {
		name            string
		inputs          []string
		trimLineEndings bool
		want            []string
	}{
		{
			name: "no trimming",
			inputs: []string{
				"single sentence\n",
				"split ",
				"sentence\n"},
			trimLineEndings: false,
			want: []string{
				"single sentence\n",
				"split sentence\n"},
		},
		{
			name: "trim LF",
			inputs: []string{
				"single sentence\n",
				"split ",
				"sentence\n"},
			trimLineEndings: true,
			want: []string{
				"single sentence",
				"split sentence"},
		},
		{
			name: "trim CRLF",
			inputs: []string{
				"single sentence\r\n",
				"split ",
				"sentence\r\n"},
			trimLineEndings: true,
			want: []string{
				"single sentence",
				"split sentence"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured := &writeCapturer{}
			lw := &lineWriter{
				next:            captured,
				trimLineEndings: tt.trimLineEndings,
			}

			for _, input := range tt.inputs {
				_, err := lw.Write([]byte(input))
				require.NoError(t, err)
			}

			require.Equal(t, tt.want, captured.writes)
		})
	}
}
