package ux

import (
	"github.com/fatih/color"
	"github.com/theckman/yacspin"
)

type Progress struct {
	spinner *yacspin.Spinner
}

func (p *Progress) Message(message string) {
	p.spinner.Message(message)
}

func (p *Progress) Warn(message string) {
	p.spinner.Pause()
	p.spinner.StopFailColors("fgYellow")
	p.spinner.StopFailCharacter("(!) Warning")
	p.spinner.StopFailMessage(message)
	p.spinner.StopFail()
}

func (p *Progress) Fail(message string) {
	p.spinner.Pause()
	p.spinner.StopFailMessage(message)
	p.spinner.StopFail()
}

func (p *Progress) WriteError(format string, args ...interface{}) {
	status := p.spinner.Status()
	if status != yacspin.SpinnerStopped {
		p.spinner.StopFail()
	}

	color.Red("ERROR: "+format, args...)
}

func (p *Progress) Write(format string, args ...interface{}) {
	status := p.spinner.Status()
	if status != yacspin.SpinnerStopped {
		p.spinner.StopFail()
	}

	color.White(format, args...)
}
