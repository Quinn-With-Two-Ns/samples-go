// Package caller hosts the caller-side workflow for the
// nexus-per-endpoint-encryption sample. A single workflow exercises both
// Nexus endpoints so a run's history shows two different at-rest encryption
// keys side by side.
package caller

import (
	"strings"

	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "nexus-per-endpoint-encryption-caller-tq"

// HelloCallerWorkflow exercises four Nexus operations -- echo + hello on
// endpoint-a, then echo + hello on endpoint-b. Each call goes through the
// NexusSerializationContext-aware codec, so its input/result are encrypted
// under that endpoint's key. A single run therefore produces history payloads
// stamped with both key-a and key-b.
func HelloCallerWorkflow(ctx workflow.Context, name string) (string, error) {
	endpoints := []string{"endpoint-a", "endpoint-b"}
	results := make([]string, 0, len(endpoints)*2)

	for _, endpoint := range endpoints {
		c := workflow.NewNexusClient(endpoint, service.HelloServiceName)

		echoFut := c.ExecuteOperation(ctx, service.EchoOperationName, service.EchoInput{Message: "echo from " + name + " via " + endpoint}, workflow.NexusOperationOptions{})
		var echoOut service.EchoOutput
		if err := echoFut.Get(ctx, &echoOut); err != nil {
			return "", err
		}

		helloFut := c.ExecuteOperation(ctx, service.HelloOperationName, service.HelloInput{Name: name + " via " + endpoint}, workflow.NexusOperationOptions{})
		var helloOut service.HelloOutput
		if err := helloFut.Get(ctx, &helloOut); err != nil {
			return "", err
		}

		results = append(results, echoOut.Message, helloOut.Message)
	}

	return strings.Join(results, " | "), nil
}
