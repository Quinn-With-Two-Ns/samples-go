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
	// via workflow.ContextAware to pick the keyID at encode time.
	clientOptions.DataConverter = nexusperendpointencryption.NewEncryptingDataConverter(
		converter.GetDefaultDataConverter(),
		nexusperendpointencryption.DataConverterOptions{Compress: true},
	)

	// One Interceptor instance, wired in two places: as a ContextPropagator on
	// the client (so workflow-start headers carry CryptContext onto the
	// workflow's root context, where result encoding can see it) and as a
	// WorkerInterceptor on the worker (so Nexus boundary calls swap in the
	// per-endpoint key).
	ix := &nexusperendpointencryption.Interceptor{
		EndpointKeys: nexusperendpointencryption.EndpointKeys,
	}
	clientOptions.ContextPropagators = []workflow.ContextPropagator{ix}

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, caller.TaskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{ix},
	})

	w.RegisterWorkflow(caller.HelloCallerWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
