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
	"go.temporal.io/sdk/worker"
)

const taskQueue = "nexus-per-endpoint-encryption-handler-tq"

func main() {
	clientOptions, err := options.ParseClientOptionFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}

	// Same Codec wiring as the caller worker. The SDK passes
	// NexusSerializationContext into the codec on the handler's Nexus task
	// (sync result encoding) and on the spawned workflow's input/output
	// (workflow-run operations), so both paths encrypt under the per-endpoint
	// key with no interceptor plumbing.
	clientOptions.DataConverter = converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&nexusperendpointencryption.Codec{},
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}),
	)

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{})

	svc := nexus.NewService(service.HelloServiceName)
	if err := svc.Register(handler.EchoOperation, handler.HelloOperation); err != nil {
		log.Fatalln("Unable to register operations", err)
	}
	w.RegisterNexusService(svc)
	w.RegisterWorkflow(handler.HelloHandlerWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
