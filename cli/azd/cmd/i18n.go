// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"embed"
	"log"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

var localizer *i18n.Localizer
var bundle *i18n.Bundle

//go:embed i18n/*.yaml
var localeFS embed.FS

func loadLocalizer() {
	bundle = i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)
	_, err := bundle.LoadMessageFileFS(localeFS, "i18n/en.yaml")
	if err != nil {
		log.Panicf("failed loading localizer: %s", err)
	}
	localizer = i18n.NewLocalizer(bundle, language.English.String())
}

func i18nGetText(id i18nTextId) string {
	if localizer == nil {
		loadLocalizer()
	}
	config := &i18n.LocalizeConfig{
		MessageID: string(id),
	}
	return localizer.MustLocalize(config)
}

type i18nTextId string

const (
	i18nProductName          i18nTextId = "productName"
	i18nDocsProductName      i18nTextId = "docsProductName"
	i18nAzdShortHelp         i18nTextId = "azdShortHelp"
	i18nUsage                i18nTextId = "usage"
	i18nAzdUsage             i18nTextId = "azdUsage"
	i18nCommands             i18nTextId = "commands"
	i18nCmdGroupTitleConfig  i18nTextId = "cmdGroupTitleConfig"
	i18nCmdGroupTitleManage  i18nTextId = "cmdGroupTitleManage"
	i18nCmdGroupTitleMonitor i18nTextId = "cmdGroupTitleMonitor"
	i18nCmdGroupTitleAbout   i18nTextId = "cmdGroupTitleAbout"
	i18nCmdHelp              i18nTextId = "cmdHelp"
	i18nHelp                 i18nTextId = "help"
	i18nFlags                i18nTextId = "flags"
)
