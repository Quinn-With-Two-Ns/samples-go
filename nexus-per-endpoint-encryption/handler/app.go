// Package handler exposes the handler-side Nexus operations and workflow.
package handler

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"

	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
)

// EchoOperation is a sync Nexus operation. The handler returns immediately
// without starting a workflow. The handler-side inbound interceptor wraps the
// result in EndpointAware so the encrypting DataConverter can recover the
// target endpoint at ToPayload time -- the sync response has no workflow
// start header to carry CryptContext through.
var EchoOperation = nexus.NewSyncOperation(
	service.EchoOperationName,
	func(ctx context.Context, input service.EchoInput, _ nexus.StartOperationOptions) (service.EchoOutput, error) {
		return service.EchoOutput{Message: input.Message}, nil
	},
)

// HelloOperation is a workflow-run Nexus operation. The handler-started
// workflow inherits CryptContext via the workflow start header that the
// ContextPropagator stamps, so its at-rest payloads and result encode under
// the per-endpoint key.
var HelloOperation = temporalnexus.NewWorkflowRunOperation(
	service.HelloOperationName,
	HelloHandlerWorkflow,
	func(ctx context.Context, input service.HelloInput, options nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{
			// Use the request ID allocated by Temporal so retries dedupe to
			// the same workflow.
			ID: "callee_" + options.RequestID,
		}, nil
	},
)

func HelloHandlerWorkflow(ctx workflow.Context, input service.HelloInput) (service.HelloOutput, error) {
	return service.HelloOutput{Message: "Hello " + input.Name + "!"}, nil
}
