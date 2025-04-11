package internal

import (
	"github.com/fatih/color"
)

func WriteCommandHeader(header string, description string) {
	color.HiWhite(header)
	if description != "" {
		color.HiBlack(description)
	}
}

func WriteCommandSuccess(message string) {
	color.Green("SUCCESS: %s", message)
}
