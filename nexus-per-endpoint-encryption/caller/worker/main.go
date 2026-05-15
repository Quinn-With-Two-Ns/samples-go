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

	// The caller-side outbound interceptor stamps the target endpoint into
	// every Nexus call's header and sets CryptContext on the workflow.Context
	// for the duration of the call so the operation input encodes under the
	// per-endpoint key. The same Interceptor is registered as a client
	// interceptor so client-side ExecuteWorkflow / Query / Update / Signal
	// also carry the endpoint as a header.
	ix := &nexusperendpointencryption.Interceptor{
		EndpointKeys: nexusperendpointencryption.EndpointKeys,
	}
	clientOptions.Interceptors = append(clientOptions.Interceptors, ix)

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
