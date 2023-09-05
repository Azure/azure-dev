package middleware

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/experimentation"
)

type ExperimentationMiddleware struct {
}

func NewExperimentationMiddleware() Middleware {
	return &ExperimentationMiddleware{}
}

const assignmentEndpoint = "https://default.exp-tas.com/exptas49/b80dfe81-554e-48ec-a7bc-1dd773cd6a54-azdexpws/api/v1/tas"

func (m *ExperimentationMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	endpoint := assignmentEndpoint
	// Allow overriding the assignment endpoint, either for local development (where you want to hit a private instance)
	// or testing (we use this in our end to end tests to control assignment behavior for the CLI under test)/
	if override := os.Getenv("AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT"); override != "" {
		log.Printf("using override assignment endpoint: %s, from AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT", override)
		endpoint = override
	}

	if assignmentManager, err := experimentation.NewAssignmentsManager(
		endpoint,
		http.DefaultClient,
	); err == nil {
		if assignment, err := assignmentManager.Assignment(ctx); err != nil {
			log.Printf("failed to get variant assignments: %v", err)
		} else {
			log.Printf("assignment context: %v", assignment.AssignmentContext)
			tracing.SetGlobalAttributes(fields.ExpAssignmentContextKey.String(assignment.AssignmentContext))

			for _, feature := range assignment.Configs {
				for parameter, value := range feature.Parameters {
					// Apply any alpha feature configuration from set parameters
					if strings.HasPrefix(parameter, "alpha_") {
						featureId := parameter[len("alpha_"):]
						if enablement, ok := value.(bool); ok {
							// set default enablement for the feature
							alpha.SetDefaultEnablement(featureId, enablement)
						} else {
							log.Printf("could not parse value for alpha feature '%s' as a bool, ignoring", featureId)
						}
					}
				}
			}
		}
	} else {
		log.Printf("failed to create assignment manager: %v", err)
	}

	return next(ctx)
}
