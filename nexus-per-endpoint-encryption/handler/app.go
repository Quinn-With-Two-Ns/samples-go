// Package handler exposes the handler-side Nexus operation and workflow.
package handler

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"

	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
)

// HelloOperation is a workflow-run Nexus operation. The handler-started
// workflow inherits the active CryptContext (seeded by the inbound
// interceptor) via the registered ContextPropagator, so its at-rest payloads
// are encrypted under the per-endpoint key.
var HelloOperation = temporalnexus.NewWorkflowRunOperation(
	service.HelloOperationName,
	HelloHandlerWorkflow,
	func(ctx context.Context, input service.HelloInput, options nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{
			// Use the request ID allocated by Temporal so that retries
			// dedupe to the same workflow.
			ID: options.RequestID,
		}, nil
	},
)

func HelloHandlerWorkflow(ctx workflow.Context, input service.HelloInput) (service.HelloOutput, error) {
	return service.HelloOutput{Message: "Hello " + input.Name + "!"}, nil
}
