package mockarmresources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
)

var tagFilterExpression = regexp.MustCompile("tagName eq '(.+)' and tagValue eq '(.*?)'")

var nameFilterExpression = regexp.MustCompile("name eq '(.+)'")

func AddAzResourceListMock(
	c *mockhttp.MockHttpClient,
	matchResourceGroupName *string,
	result []*armresources.GenericResourceExpanded,
) {
	c.When(func(request *http.Request) bool {
		isMatch := strings.Contains(request.URL.Path, "/resources")
		if matchResourceGroupName != nil {
			isMatch = isMatch &&
				strings.Contains(request.URL.Path, fmt.Sprintf("/resourceGroups/%s/resources", *matchResourceGroupName))
		}

		return isMatch
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		requestResult := result

		if filter := request.URL.Query().Get("$filter"); filter != "" {
			requestResult = applyFilter(filter, result)
		}

		jsonBytes, err := json.Marshal(armresources.ResourceListResult{
			Value: requestResult,
		})
		if err != nil {
			panic(err)
		}

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})
}

func applyFilter(filter string, result []*armresources.GenericResourceExpanded) []*armresources.GenericResourceExpanded {
	var tagNameFilter string
	var tagValueFilter string
	var nameFilter string

	matches := tagFilterExpression.FindStringSubmatch(filter)

	if len(matches) >= 3 {
		tagNameFilter = matches[1]
		tagValueFilter = matches[2]
	}

	matches = nameFilterExpression.FindStringSubmatch(filter)
	if len(matches) >= 2 {
		nameFilter = matches[1]
	}

	filteredResult := []*armresources.GenericResourceExpanded{}
	for _, resource := range result {
		if tagNameFilter != "" {
			tagVal := resource.Tags[tagNameFilter]
			if tagVal == nil {
				// treat nil as empty string
				tagVal = convert.RefOf("")
			}

			if tagValueFilter != *tagVal {
				continue
			}
		}

		if nameFilter != "" {
			name := resource.Name
			if name == nil {
				// treat nil as empty string
				name = convert.RefOf("")
			}

			if *name != nameFilter {
				continue
			}
		}

		filteredResult = append(filteredResult, resource)
	}

	return filteredResult
}
