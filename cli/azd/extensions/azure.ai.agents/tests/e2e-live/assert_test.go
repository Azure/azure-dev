// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package e2elive

import "testing"

func TestResponseHasExpectedAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain four digit", "The answer is 4.", true},
		{"bare four", "4", true},
		{"equation", "2+2=4", true},
		{"spelled word", "It is four.", true},
		{"spelled upper", "FOUR", true},
		{"parenthesized", "(4)", true},
		{"trailing period mid-sentence", "the value 4. is final", true},
		{"model name", "gpt-4o-mini", false},
		{"version", "4.1", false},
		{"decimal four", "4.0", false}, // intentional: see responseHasExpectedAnswer doc
		{"status code", "404", false},
		{"price", "$40", false},
		{"ratio", "24/7", false},
		{"fourteen", "fourteen apples", false},
		{"no answer", "I am not sure", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(tc.text); got != tc.want {
				t.Errorf("responseHasExpectedAnswer(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestAgentResponseRegion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want bool // responseHasExpectedAnswer over the sliced region
	}{
		{
			"answer scoped between markers",
			"using model gpt-4o-mini\n[agent] The answer is 4.\nServer responded in 2s (first byte: 1s)\n",
			true,
		},
		{
			"stray digits outside region rejected",
			"gpt-4o-mini deployed (404 cached)\n[agent] I am not sure.\nServer responded in 4.0s\n",
			false,
		},
		{
			"standalone 4 before agent line excluded by region",
			"completed step 4\n[agent] I don't know.\nServer responded in 1s\n",
			false,
		},
		{
			"missing footer falls back to full text",
			"using gpt-4o-mini\n[agent] four",
			true,
		},
		{
			"no agent line falls back to full text",
			"the answer is four",
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(agentResponseRegion(tc.out)); got != tc.want {
				t.Errorf("region(%q) -> %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
