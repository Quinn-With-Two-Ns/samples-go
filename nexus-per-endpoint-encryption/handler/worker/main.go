package main

import (
	"log"
	"os"

	"github.com/nexus-rpc/sdk-go/nexus"
	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/handler"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
	"github.com/temporalio/samples-go/nexus/options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const taskQueue = "nexus-per-endpoint-encryption-handler-tq"

func main() {
	clientOptions, err := options.ParseClientOptionFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}
	// Same encrypting DataConverter wiring as the caller worker. The handler
	// workflow inherits CryptContext from the inbound interceptor via the
	// ContextPropagator and so encrypts its own at-rest payloads under the
	// per-endpoint key.
	clientOptions.DataConverter = nexusperendpointencryption.NewEncryptingDataConverter(
		converter.GetDefaultDataConverter(),
		nexusperendpointencryption.DataConverterOptions{Compress: true},
	)
	clientOptions.ContextPropagators = []workflow.ContextPropagator{nexusperendpointencryption.NewContextPropagator()}

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{
			// The inbound interceptor reads
			// temporalnexus.GetOperationInfo(ctx).Endpoint and seeds
			// CryptContext on the Go context, which the
			// ContextPropagator then carries into the handler-started
			// workflow.
			&nexusperendpointencryption.WorkerInterceptor{EndpointKeys: nexusperendpointencryption.EndpointKeys},
		},
	})

	svc := nexus.NewService(service.HelloServiceName)
	if err := svc.Register(handler.HelloOperation); err != nil {
		log.Fatalln("Unable to register operations", err)
	}
	w.RegisterNexusService(svc)
	w.RegisterWorkflow(handler.HelloHandlerWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
