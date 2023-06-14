package recording

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

var subscriptionDeploymentUrl = regexp.MustCompile(
	`subscriptions\/[a-f0-9A-F-]+\/providers\/Microsoft\.Resources\/deployments\?`)

func TrimSubscriptionsDeployment(i *cassette.Interaction, variables map[string]string) error {
	url, err := url.Parse(i.Request.URL)
	if err != nil {
		return err
	}

	if i.Request.Method != http.MethodGet {
		return nil
	}

	if !subscriptionDeploymentUrl.Match([]byte(url.Path)) {
		return nil
	}
	var res armresources.DeploymentListResult
	err = json.Unmarshal([]byte(i.Response.Body), &res)
	if err != nil {
		return err
	}

	// Filter to the subscriptions matching the current environment
	filtered := []*armresources.DeploymentExtended{}
	for _, deployment := range res.Value {
		if deployment.Tags != nil {
			envTag := deployment.Tags[azure.TagKeyAzdEnvName]
			if envTag != nil && *envTag == variables[EnvNameKey] {
				filtered = append(filtered, deployment)
			}
		}
	}

	res.Value = filtered
	content, err := json.Marshal(res)
	if err != nil {
		return err
	}
	i.Response.Body = string(content)
	return nil
}
