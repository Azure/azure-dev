package ux

import (
	"strings"
)

type InputHint struct {
	Title string

	Text string

	Examples []string
}

func (i InputHint) ToString() string {
	sb := strings.Builder{}
	sb.WriteString(i.Title)
	sb.WriteString("\n")
	sb.WriteString(i.Text)

	if len(i.Text) > 0 && i.Text[len(i.Text)-1:] != "\n" {
		sb.WriteString("\n")
	}

	if len(i.Examples) > 0 {
		sb.WriteString("\n")
		sb.WriteString("Examples:\n  ")
		sb.WriteString(strings.Join(i.Examples, "\n  "))
		sb.WriteString("\n")
	}

	return sb.String()
}
