// Package caller hosts the caller-side workflow for the
// nexus-per-endpoint-encryption sample. The workflow takes the target Nexus
// endpoint as input so the same workflow can be exercised over each endpoint
// and produce observably different at-rest encryption keys on the wire.
package caller

import (
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "nexus-per-endpoint-encryption-caller-tq"

// HelloCallerWorkflow exercises both Nexus operation types over the supplied
// endpoint:
//
//  1. EchoOperation (sync) -- demonstrates the wire-boundary encryption with
//     no handler-side workflow involved.
//  2. HelloOperation (workflow-run) -- demonstrates wire-boundary encryption
//     plus per-endpoint at-rest encryption in the handler's namespace.
//
// The endpoint name flows into the WorkflowOutboundInterceptor, which selects
// the per-endpoint encryption key for each operation invocation.
func HelloCallerWorkflow(ctx workflow.Context, endpoint, name string) (string, error) {
	c := workflow.NewNexusClient(endpoint, service.HelloServiceName)

	echoFut := c.ExecuteOperation(ctx, service.EchoOperationName, service.EchoInput{Message: "echo from " + name}, workflow.NexusOperationOptions{})
	var echoOut service.EchoOutput
	if err := echoFut.Get(ctx, &echoOut); err != nil {
		return "", err
	}

	helloFut := c.ExecuteOperation(ctx, service.HelloOperationName, service.HelloInput{Name: name}, workflow.NexusOperationOptions{})
	var helloOut service.HelloOutput
	if err := helloFut.Get(ctx, &helloOut); err != nil {
		return "", err
	}

	return echoOut.Message + " | " + helloOut.Message, nil
}
