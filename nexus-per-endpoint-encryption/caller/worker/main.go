package main

import (
	"log"
	"os"

	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/caller"
	"github.com/temporalio/samples-go/nexus/options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func main() {
	clientOptions, err := options.ParseClientOptionFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}

	// Encrypting DataConverter: reads CryptContext from workflow / Go context
	// via workflow.ContextAware to select the keyID at encode time.
	clientOptions.DataConverter = nexusperendpointencryption.NewEncryptingDataConverter(
		converter.GetDefaultDataConverter(),
		nexusperendpointencryption.DataConverterOptions{Compress: true},
	)
	// ContextPropagator carries CryptContext from the Go context (seeded by
	// the starter) into the caller workflow's headers and on into any
	// activities or child constructs.
	clientOptions.ContextPropagators = []workflow.ContextPropagator{nexusperendpointencryption.NewContextPropagator()}

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, caller.TaskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{
			// The WorkerInterceptor's outbound interceptor reads
			// input.Client.Endpoint() and seeds CryptContext on the
			// workflow context for the duration of the Nexus call.
			&nexusperendpointencryption.WorkerInterceptor{EndpointKeys: nexusperendpointencryption.EndpointKeys},
		},
	})

	w.RegisterWorkflow(caller.HelloCallerWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
