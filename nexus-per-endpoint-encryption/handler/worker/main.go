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
)

const taskQueue = "nexus-per-endpoint-encryption-handler-tq"

func main() {
	clientOptions, err := options.ParseClientOptionFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}

	// Same encrypting DataConverter wiring as the caller worker.
	clientOptions.DataConverter = nexusperendpointencryption.NewEncryptingDataConverter(
		converter.GetDefaultDataConverter(),
		nexusperendpointencryption.DataConverterOptions{Compress: true},
	)

	// The handler's Nexus inbound interceptor reads
	// temporalnexus.GetOperationInfo(ctx).Endpoint and seeds CryptContext on
	// the Go context. When temporalnexus.NewWorkflowRunOperation then issues
	// client.ExecuteWorkflow under that ctx, the Client-side interceptor
	// stamps the keyID onto the handler workflow's start headers; the
	// Worker-side interceptor reads it back at ExecuteWorkflow time.
	ix := &nexusperendpointencryption.Interceptor{
		EndpointKeys: nexusperendpointencryption.EndpointKeys,
	}
	clientOptions.Interceptors = []interceptor.ClientInterceptor{ix}

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{ix},
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
