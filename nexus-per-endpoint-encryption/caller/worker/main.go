package main

import (
	"log"
	"os"

	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/caller"
	"github.com/temporalio/samples-go/nexus/options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
)

func main() {
	clientOptions, err := options.ParseClientOptionFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}

	// The Codec implements PayloadCodecWithSerializationContext, so the SDK
	// hands it a NexusSerializationContext (carrying the endpoint name) at
	// every Nexus serialization boundary. Inside CodecDataConverter the codec
	// then selects the per-endpoint key.
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

	w := worker.New(c, caller.TaskQueue, worker.Options{})
	w.RegisterWorkflow(caller.HelloCallerWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
