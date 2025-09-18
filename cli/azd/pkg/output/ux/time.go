// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"strings"
	"time"
)

// DurationAsText provides a slightly nicer string representation of a duration
// when compared to default formatting in go, by spelling out the words hour,
// minute and second and providing some spacing and eliding the fractional component
// of the seconds part.
func DurationAsText(d time.Duration) string {
	if d.Seconds() < 1.0 {
		return "less than a second"
	}

	var builder strings.Builder

	if (d / time.Hour) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Hour), "hour")
		d = d - ((d / time.Hour) * time.Hour)
	}

	if (d / time.Minute) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Minute), "minute")
		d = d - ((d / time.Minute) * time.Minute)
	}

	if (d / time.Second) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Second), "second")
	}

	return builder.String()
}

// writePart writes the string [part] followed by [unit] into [builder], unless
// part is empty or the string "0". If part is "1", the [unit] string is suffixed
// with s. If builder is non empty, the written string is preceded by a space.
func writePart(builder *strings.Builder, part string, unit string) {
	if part != "" && part != "0" {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}

		builder.WriteString(part)
		builder.WriteByte(' ')
		builder.WriteString(unit)
		if part != "1" {
			builder.WriteByte('s')
		}
	}
}
