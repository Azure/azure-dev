// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationAsText(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			"sub second",
			500 * time.Millisecond,
			"less than a second",
		},
		{
			"zero duration",
			0,
			"less than a second",
		},
		{
			"one second singular",
			1 * time.Second,
			"1 second",
		},
		{
			"multiple seconds",
			5 * time.Second,
			"5 seconds",
		},
		{
			"one minute singular",
			1 * time.Minute,
			"1 minute",
		},
		{
			"minutes and seconds",
			2*time.Minute + 30*time.Second,
			"2 minutes 30 seconds",
		},
		{
			"one hour singular",
			1 * time.Hour,
			"1 hour",
		},
		{
			"hours minutes seconds",
			2*time.Hour + 3*time.Minute + 4*time.Second,
			"2 hours 3 minutes 4 seconds",
		},
		{
			"exact minutes no seconds",
			5 * time.Minute,
			"5 minutes",
		},
		{
			"hour and seconds no minutes",
			1*time.Hour + 10*time.Second,
			"1 hour 10 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := durationAsText(tt.duration)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWritePart(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		part     string
		unit     string
		want     string
	}{
		{
			"singular unit",
			"", "1", "hour",
			"1 hour",
		},
		{
			"plural unit",
			"", "5", "minute",
			"5 minutes",
		},
		{
			"empty part skipped",
			"", "", "second",
			"",
		},
		{
			"zero part skipped",
			"", "0", "hour",
			"",
		},
		{
			"appends with space",
			"1 hour", "30", "minute",
			"1 hour 30 minutes",
		},
		{
			"singular after existing",
			"2 hours", "1", "minute",
			"2 hours 1 minute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var builder strings.Builder
			builder.WriteString(tt.existing)
			writePart(&builder, tt.part, tt.unit)
			assert.Equal(t, tt.want, builder.String())
		})
	}
}

func TestGetBooleanString(t *testing.T) {
	assert.Equal(t, "Yes", getBooleanString(true))
	assert.Equal(t, "No", getBooleanString(false))
}

func TestParseBooleanString(t *testing.T) {
	yesInputs := []string{
		"y", "yes", "true", "1",
		"Y", "YES", "True", "TRUE",
	}
	for _, input := range yesInputs {
		t.Run("yes_"+input, func(t *testing.T) {
			got, err := parseBooleanString(input)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.True(t, *got)
		})
	}

	noInputs := []string{
		"n", "no", "false", "0",
		"N", "NO", "False", "FALSE",
	}
	for _, input := range noInputs {
		t.Run("no_"+input, func(t *testing.T) {
			got, err := parseBooleanString(input)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.False(t, *got)
		})
	}

	invalidInputs := []string{
		"maybe", "yep", "nope", "2", "",
		"absolutely", "x",
	}
	for _, input := range invalidInputs {
		t.Run("invalid_"+input, func(t *testing.T) {
			got, err := parseBooleanString(input)
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestTaskStateConstants(t *testing.T) {
	assert.Equal(t, TaskState(0), Pending)
	assert.Equal(t, TaskState(1), Running)
	assert.Equal(t, TaskState(2), Skipped)
	assert.Equal(t, TaskState(3), Warning)
	assert.Equal(t, TaskState(4), Error)
	assert.Equal(t, TaskState(5), Success)
}
