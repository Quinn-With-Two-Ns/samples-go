// Package caller hosts the caller-side workflow for the
// nexus-per-endpoint-encryption sample. The workflow takes the target Nexus
// endpoint as an input so the same workflow can be exercised over each
// endpoint and produce observably different at-rest encryption keys.
package caller

import (
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "nexus-per-endpoint-encryption-caller-tq"

// HelloCallerWorkflow calls the configured Nexus operation over the supplied
// endpoint. The endpoint name flows into the WorkerInterceptor, which selects
// the per-endpoint encryption key.
func HelloCallerWorkflow(ctx workflow.Context, endpoint, name string) (string, error) {
	c := workflow.NewNexusClient(endpoint, service.HelloServiceName)

	fut := c.ExecuteOperation(ctx, service.HelloOperationName, service.HelloInput{Name: name}, workflow.NexusOperationOptions{})
	var out service.HelloOutput
	if err := fut.Get(ctx, &out); err != nil {
		return "", err
	}
	return out.Message, nil
}
