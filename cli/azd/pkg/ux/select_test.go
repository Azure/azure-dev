// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Select tests ---

func TestNewSelect_with_choices(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Pick one",
		Choices: []*SelectChoice{
			{Value: "a", Label: "Option A"},
			{Value: "b", Label: "Option B"},
		},
	})
	require.NotNil(t, s)
	assert.Len(t, s.choices, 2)
	assert.Len(t, s.filteredChoices, 2)
	assert.Equal(t, "Pick one", s.options.Message)
}

func TestNewSelect_default_hint(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Pick one",
		Choices: []*SelectChoice{
			{Value: "a", Label: "A"},
		},
	})
	assert.Contains(t, s.options.Hint, "Use arrows to move")
	assert.Contains(t, s.options.Hint, "type to filter")
}

func TestNewSelect_hint_no_filter(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:          io.Discard,
		Message:         "Pick one",
		Choices:         []*SelectChoice{{Value: "a", Label: "A"}},
		EnableFiltering: new(false),
	})
	assert.Contains(t, s.options.Hint, "Use arrows to move")
	assert.NotContains(t, s.options.Hint, "type to filter")
}

func TestNewSelect_custom_hint(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Pick one",
		Choices: []*SelectChoice{{Value: "a", Label: "A"}},
		Hint:    "[my hint]",
	})
	assert.Equal(t, "[my hint]", s.options.Hint)
}

func TestSelect_Render_initial(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "a", Label: "Alpha"},
			{Value: "b", Label: "Bravo"},
			{Value: "c", Label: "Charlie"},
		},
	})

	err := s.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Choose")
	assert.Contains(t, output, "Alpha")
	assert.Contains(t, output, "Bravo")
	assert.Contains(t, output, "Charlie")
}

func TestSelect_Render_complete(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "a", Label: "Alpha"},
		},
	})
	s.complete = true
	s.selectedChoice = s.choices[0]

	err := s.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Choose")
	assert.Contains(t, output, "Alpha")
	// Options list should NOT appear when complete
	assert.NotContains(t, output, "Use arrows")
}

func TestSelect_Render_cancelled(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "a", Label: "Alpha"},
		},
	})
	s.cancelled = true

	err := s.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Cancelled")
}

func TestSelect_applyFilter_matches(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "apple", Label: "Apple"},
			{Value: "banana", Label: "Banana"},
			{Value: "apricot", Label: "Apricot"},
		},
	})
	// Initialize currentIndex (Render normally does this)
	s.currentIndex = new(0)
	s.filter = "ap"

	s.applyFilter()
	assert.Len(t, s.filteredChoices, 2)
}

func TestSelect_applyFilter_no_match(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "apple", Label: "Apple"},
			{Value: "banana", Label: "Banana"},
		},
	})
	s.currentIndex = new(0)
	s.filter = "xyz"

	s.applyFilter()
	assert.Empty(t, s.filteredChoices)
}

func TestSelect_applyFilter_empty_resets(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "a", Label: "A"},
			{Value: "b", Label: "B"},
		},
	})
	s.currentIndex = new(0)
	s.filter = ""

	s.applyFilter()
	assert.Len(t, s.filteredChoices, 2)
}

func TestSelect_applyFilter_by_number(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:         io.Discard,
		Message:        "Choose",
		DisplayNumbers: new(true),
		Choices: []*SelectChoice{
			{Value: "a", Label: "Alpha"},
			{Value: "b", Label: "Bravo"},
			{Value: "c", Label: "Charlie"},
		},
	})
	s.currentIndex = new(0)
	s.filter = "2"

	s.applyFilter()
	assert.Len(t, s.filteredChoices, 1)
	assert.Equal(t, "Bravo", s.filteredChoices[0].Label)
}

func TestSelect_WithCanvas(t *testing.T) {
	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{{Value: "a", Label: "A"}},
	})
	var buf bytes.Buffer
	c := NewCanvas().WithWriter(&buf)
	defer c.Close()

	result := s.WithCanvas(c)
	assert.Equal(t, s, result)
}

func TestSelect_renderValidation_no_matches(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSelect(&SelectOptions{
		Writer:  io.Discard,
		Message: "Choose",
		Choices: []*SelectChoice{
			{Value: "a", Label: "A"},
		},
	})
	s.filteredChoices = []*indexedSelectChoice{}

	s.renderValidation(printer)
	assert.True(t, s.hasValidationError)
	assert.Contains(t, s.validationMessage, "No options found")
}

// --- MultiSelect tests ---

func TestNewMultiSelect_with_choices(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
			{Value: "b", Label: "Bravo"},
		},
	})
	require.NotNil(t, ms)
	assert.Len(t, ms.choices, 2)
	assert.Empty(t, ms.selectedChoices)
}

func TestNewMultiSelect_preselected(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha", Selected: true},
			{Value: "b", Label: "Bravo"},
			{Value: "c", Label: "Charlie", Selected: true},
		},
	})
	assert.Len(t, ms.selectedChoices, 2)
	assert.Contains(t, ms.selectedChoices, "a")
	assert.Contains(t, ms.selectedChoices, "c")
}

func TestMultiSelect_Render_initial(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
			{Value: "b", Label: "Bravo"},
		},
	})

	err := ms.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Pick many")
	assert.Contains(t, output, "Alpha")
	assert.Contains(t, output, "Bravo")
}

func TestMultiSelect_Render_complete(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha", Selected: true},
			{Value: "b", Label: "Bravo"},
		},
	})
	ms.complete = true

	err := ms.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Alpha")
	// Footer should NOT appear when complete
	assert.NotContains(t, output, "Use arrows")
}

func TestMultiSelect_Render_cancelled(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
		},
	})
	ms.cancelled = true

	err := ms.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Cancelled")
}

func TestMultiSelect_validate_no_selection(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
		},
	})
	ms.submitted = true
	ms.validate()

	assert.True(t, ms.hasValidationError)
	assert.Contains(t,
		ms.validationMessage, "At least one option",
	)
}

func TestMultiSelect_validate_no_selection_allowed(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
		},
		AllowEmptySelection: new(true),
	})
	ms.submitted = true
	ms.validate()

	assert.False(t, ms.hasValidationError)
	assert.Empty(t, ms.validationMessage)
}

func TestMultiSelect_validate_with_selection(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha", Selected: true},
		},
	})
	ms.submitted = true
	ms.validate()

	assert.False(t, ms.hasValidationError)
}

func TestMultiSelect_validate_empty_filter(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
		},
	})
	ms.filteredChoices = []*indexedMultiSelectChoice{}
	ms.validate()

	assert.True(t, ms.hasValidationError)
	assert.Contains(t,
		ms.validationMessage, "No options found",
	)
}

func TestMultiSelect_sortSelectedChoices(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick many",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "Alpha"},
			{Value: "b", Label: "Bravo"},
			{Value: "c", Label: "Charlie"},
		},
	})
	// Select in reverse order
	ms.selectedChoices["c"] = ms.choices[2]
	ms.selectedChoices["a"] = ms.choices[0]

	sorted := ms.sortSelectedChoices()
	require.Len(t, sorted, 2)
	assert.Equal(t, "Alpha", sorted[0].Label)
	assert.Equal(t, "Charlie", sorted[1].Label)
}

func TestMultiSelect_applyFilter(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: []*MultiSelectChoice{
			{Value: "apple", Label: "Apple"},
			{Value: "banana", Label: "Banana"},
			{Value: "apricot", Label: "Apricot"},
		},
	})
	ms.currentIndex = new(0)
	ms.filter = "ap"

	ms.applyFilter()
	assert.Len(t, ms.filteredChoices, 2)
}

func TestMultiSelect_WithCanvas(t *testing.T) {
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: []*MultiSelectChoice{
			{Value: "a", Label: "A"},
		},
	})
	var buf bytes.Buffer
	c := NewCanvas().WithWriter(&buf)
	defer c.Close()

	result := ms.WithCanvas(c)
	assert.Equal(t, ms, result)
}

// buildMultiSelectChoices returns n placeholder choices for exercising the
// length-based behavior of MultiSelect (the filter/footer thresholds).
func buildMultiSelectChoices(n int) []*MultiSelectChoice {
	choices := make([]*MultiSelectChoice, n)
	for i := range choices {
		choices[i] = &MultiSelectChoice{
			Value: fmt.Sprintf("v%d", i),
			Label: fmt.Sprintf("Option %d", i),
		}
	}
	return choices
}

func TestMultiSelect_filteringEnabled(t *testing.T) {
	// Requested by the caller but the list is shorter than the threshold.
	short := NewMultiSelect(&MultiSelectOptions{
		Writer:          io.Discard,
		Choices:         buildMultiSelectChoices(multiSelectFilterThreshold - 1),
		EnableFiltering: new(true),
	})
	assert.False(t, short.filteringEnabled(),
		"filtering should be off below the threshold")

	// Requested and the list is long enough.
	long := NewMultiSelect(&MultiSelectOptions{
		Writer:          io.Discard,
		Choices:         buildMultiSelectChoices(multiSelectFilterThreshold),
		EnableFiltering: new(true),
	})
	assert.True(t, long.filteringEnabled(),
		"filtering should be on at or above the threshold")

	// Explicitly disabled: off regardless of length.
	disabled := NewMultiSelect(&MultiSelectOptions{
		Writer:          io.Discard,
		Choices:         buildMultiSelectChoices(multiSelectFilterThreshold + 2),
		EnableFiltering: new(false),
	})
	assert.False(t, disabled.filteringEnabled(),
		"filtering should be off when the caller disables it")
}

func TestMultiSelect_renderFooter_shortList_hidesNoneAll(t *testing.T) {
	var buf bytes.Buffer
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: buildMultiSelectChoices(multiSelectFilterThreshold - 1),
	})

	ms.renderFooter(NewPrinter(&buf))

	out := buf.String()
	assert.Contains(t, out, "Move")
	assert.Contains(t, out, "Select")
	assert.Contains(t, out, "Confirm")
	// The None/All shortcut is hidden for short, easy-to-scan lists.
	assert.NotContains(t, out, "None/All")
}

func TestMultiSelect_renderFooter_longList_showsNoneAll(t *testing.T) {
	var buf bytes.Buffer
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: buildMultiSelectChoices(multiSelectFilterThreshold),
	})

	ms.renderFooter(NewPrinter(&buf))

	out := buf.String()
	assert.Contains(t, out, "None/All")
	assert.Contains(t, out, "Move")
}

func TestMultiSelect_renderFooter_help(t *testing.T) {
	// A help message adds a "? Help" shortcut to the footer.
	var withHelp bytes.Buffer
	NewMultiSelect(&MultiSelectOptions{
		Writer:      io.Discard,
		Message:     "Pick",
		Choices:     buildMultiSelectChoices(2),
		HelpMessage: "some help",
	}).renderFooter(NewPrinter(&withHelp))
	assert.Contains(t, withHelp.String(), "Help")

	// Without a help message the shortcut is omitted.
	var noHelp bytes.Buffer
	NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: buildMultiSelectChoices(2),
	}).renderFooter(NewPrinter(&noHelp))
	assert.NotContains(t, noHelp.String(), "Help")
}

func TestMultiSelect_renderFooter_skippedWhenComplete(t *testing.T) {
	var buf bytes.Buffer
	ms := NewMultiSelect(&MultiSelectOptions{
		Writer:  io.Discard,
		Message: "Pick",
		Choices: buildMultiSelectChoices(multiSelectFilterThreshold),
	})
	ms.complete = true

	ms.renderFooter(NewPrinter(&buf))

	assert.Empty(t, buf.String(), "footer must not render once complete")
}

func TestMultiSelect_renderMessage_filter(t *testing.T) {
	// Long list with filtering enabled shows the filter prompt.
	var long bytes.Buffer
	NewMultiSelect(&MultiSelectOptions{
		Writer:          io.Discard,
		Message:         "Pick",
		Choices:         buildMultiSelectChoices(multiSelectFilterThreshold),
		EnableFiltering: new(true),
	}).renderMessage(NewPrinter(&long))
	assert.Contains(t, long.String(), "Filter:")
	assert.Contains(t, long.String(), "Type to filter list")

	// Short list hides the filter prompt even when filtering is requested.
	var short bytes.Buffer
	NewMultiSelect(&MultiSelectOptions{
		Writer:          io.Discard,
		Message:         "Pick",
		Choices:         buildMultiSelectChoices(multiSelectFilterThreshold - 1),
		EnableFiltering: new(true),
	}).renderMessage(NewPrinter(&short))
	assert.NotContains(t, short.String(), "Filter:")
}
