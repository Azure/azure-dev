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
	config := &i18n.LocalizeConfig{
		MessageID: string(id),
	}
	return i18nGetTextWithConfig(config)
}

func i18nGetTextWithConfig(config *i18n.LocalizeConfig) string {
	if localizer == nil {
		loadLocalizer()
	}
	return localizer.MustLocalize(config)
}

type i18nTextId string

const (
	i18nProductName                       i18nTextId = "productName"
	i18nDocsProductName                   i18nTextId = "docsProductName"
	i18nAzdShortHelp                      i18nTextId = "azdShortHelp"
	i18nUsage                             i18nTextId = "usage"
	i18nAzdUsage                          i18nTextId = "azdUsage"
	i18nCommands                          i18nTextId = "commands"
	i18nCmdGroupTitleConfig               i18nTextId = "cmdGroupTitleConfig"
	i18nCmdGroupTitleManage               i18nTextId = "cmdGroupTitleManage"
	i18nCmdGroupTitleMonitor              i18nTextId = "cmdGroupTitleMonitor"
	i18nCmdGroupTitleAbout                i18nTextId = "cmdGroupTitleAbout"
	i18nCmdHelp                           i18nTextId = "cmdHelp"
	i18nHelp                              i18nTextId = "help"
	i18nFlags                             i18nTextId = "flags"
	i18nUse                               i18nTextId = "use"
	i18nAzd                               i18nTextId = "azd"
	i18nCommand                           i18nTextId = "command"
	i18nCmdRootHelpFooterTitle            i18nTextId = "cmdRootHelpFooterTitle"
	i18nCmdRootHelpFooterQuickStart       i18nTextId = "cmdRootHelpFooterQuickStart"
	i18nCmdRootHelpFooterQuickStartDetail i18nTextId = "cmdRootHelpFooterQuickStartDetail"
	i18nAzdUpTemplate                     i18nTextId = "azdUpTemplate"
	i18nTemplateName                      i18nTextId = "templateName"
	i18nCmdRootHelpFooterQuickStartLast   i18nTextId = "cmdRootHelpFooterQuickStartLast"
	i18nCmdRootHelpFooterQuickStartNote   i18nTextId = "cmdRootHelpFooterQuickStartNote"
	i18nAwesomeAzdUrl                     i18nTextId = "awesomeAzdUrl"
	i18nAzdUpNodeJsMongo                  i18nTextId = "azdUpNodeJsMongo"
	i18nCmdRootHelpFooterReportBug        i18nTextId = "cmdRootHelpFooterReportBug"
	i18nAzdHats                           i18nTextId = "azdHats"
	i18nCmdConfigShort                    i18nTextId = "cmdConfigShort"
	i18nCmdInitShort                      i18nTextId = "cmdInitShort"
	i18nCmdLoginShort                     i18nTextId = "cmdLoginShort"
	i18nCmdLogoutShort                    i18nTextId = "cmdLogoutShort"
	i18nCmdRestoreShort                   i18nTextId = "cmdRestoreShort"
	i18nCmdDeployShort                    i18nTextId = "cmdDeployShort"
	i18nCmdDownShort                      i18nTextId = "cmdDownShort"
	i18nCmdEnvShort                       i18nTextId = "cmdEnvShort"
	i18nCmdInfraShort                     i18nTextId = "cmdInfraShort"
	i18nCmdMonitorShort                   i18nTextId = "cmdMonitorShort"
	i18nCmdProvisionShort                 i18nTextId = "cmdProvisionShort"
	i18nCmdUpShort                        i18nTextId = "cmdUpShort"
	i18nCmdPipelineShort                  i18nTextId = "cmdPipelineShort"
)
