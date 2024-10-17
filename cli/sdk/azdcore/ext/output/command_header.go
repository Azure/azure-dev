package output

import (
	"fmt"

	"github.com/azure/azure-dev/cli/sdk/azdcore/ux"
	"github.com/fatih/color"
)

type CommandHeader struct {
	Title       string
	Description string
}

func (ch CommandHeader) Print() {
	color.White(ux.BoldString(ch.Title))
	if ch.Description != "" {
		color.HiBlack(ch.Description)
	}
	fmt.Println()
}
