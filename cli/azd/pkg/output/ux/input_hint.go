package ux

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type InputHint struct {
	Title string

	Text string

	Examples []string
}

func (i InputHint) ToString() string {
	sb := strings.Builder{}
	sb.WriteString(output.WithBold(i.Title))
	sb.WriteString("\n")
	sb.WriteString(i.Text)

	if len(i.Text) > 0 && i.Text[len(i.Text)-1:] != "\n" {
		sb.WriteString("\n")
	}

	if len(i.Examples) > 0 {
		sb.WriteString("\n")
		sb.WriteString(output.WithBold("Examples:\n  "))
		sb.WriteString(strings.Join(i.Examples, "\n  "))
		sb.WriteString("\n")
	}

	return sb.String()
}
