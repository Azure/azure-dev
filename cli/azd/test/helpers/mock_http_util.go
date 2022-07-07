package helpers

import "github.com/azure/azure-dev/cli/azd/pkg/httpUtil"

type MockHttpUtil struct {
	SendRequestFn func(req *httpUtil.HttpRequestMessage) (*httpUtil.HttpResponseMessage, error)
}

func (hu *MockHttpUtil) Send(req *httpUtil.HttpRequestMessage) (*httpUtil.HttpResponseMessage, error) {
	return hu.SendRequestFn(req)
}
