package devcentersdk

// Environments
type EnvironmentListRequestBuilder struct {
	*EntityListRequestBuilder[EnvironmentListRequestBuilder]
}

func NewEnvironmentListRequestBuilder(c *devCenterClient) *EnvironmentListRequestBuilder {
	builder := &EnvironmentListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)

	return builder
}
