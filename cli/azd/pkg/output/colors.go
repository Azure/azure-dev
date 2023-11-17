package output

import (
	"fmt"

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

func WithUnderline(text string, a ...interface{}) string {
	format := color.New(color.Underline)
	return format.Sprintf(text, a...)
}

// WithBackticks wraps text with the backtick (`) character.
func WithBackticks(text string) string {
	return "`" + text + "`"
}

// WithHyperlink wraps text with the colored hyperlink format escape sequence.
// When url and displayName are specified the displayName is used as the link display name
// When url is an empty string the url is used as the link and the display name
func WithHyperlink(url string, displayName string) string {
	if displayName == "" {
		displayName = url
	}

	var urlOutput string

	if color.NoColor {
		urlOutput = url
	} else {
		urlOutput = fmt.Sprintf("\033]8;;%s\007%s\033]8;;\007", url, displayName)
	}

	return WithLinkFormat(urlOutput)
}
