package output

import (
	"github.com/fatih/color"
)

// withLinkFormat creates string with hyperlink-looking color
func WithLinkFormat(link string, a ...interface{}) string {
	return color.HiCyanString(link, a...)
}

// withHighLightFormat creates string with highlight-looking color
func WithHighLightFormat(text string, a ...interface{}) string {
	return color.CyanString(text, a...)
}

func WithErrorFormat(text string, a ...interface{}) string {
	return color.RedString(text, a...)
}

func WithWarningFormat(text string, a ...interface{}) string {
	return color.YellowString(text, a...)
}

func WithSuccessFormat(text string, a ...interface{}) string {
	return color.GreenString(text, a...)
}

func WithGrayFormat(text string, a ...interface{}) string {
	return color.HiBlackString(text, a...)
}

func WithBold(text string, a ...interface{}) string {
	format := color.New(color.Bold)
	return format.Sprintf(text, a...)
}

// WithBackticks wraps text with the backtick (`) character.
func WithBackticks(text string) string {
	return "`" + text + "`"
}
