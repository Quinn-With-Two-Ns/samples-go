package main

import (
	"context"
	"log"
	"os"
	"time"

	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/caller"
	"github.com/temporalio/samples-go/nexus/options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
)

func main() {
	// Split out our own flag (-endpoint) before handing the remainder to the
	// shared client option parser.
	endpointFlag := ""
	nameFlag := "Nexus"
	remaining := []string{}
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-endpoint", "--endpoint":
			if i+1 >= len(args) {
				log.Fatalf("missing value for %s", args[i])
			}
			endpointFlag = args[i+1]
			i++
		case "-name", "--name":
			if i+1 >= len(args) {
				log.Fatalf("missing value for %s", args[i])
			}
			nameFlag = args[i+1]
			i++
		default:
			remaining = append(remaining, args[i])
		}
	}

	clientOptions, err := options.ParseClientOptionFlags(remaining)
	if err != nil {
		log.Fatalf("Invalid arguments: %v", err)
	}
	clientOptions.DataConverter = nexusperendpointencryption.NewEncryptingDataConverter(
		converter.GetDefaultDataConverter(),
		nexusperendpointencryption.DataConverterOptions{Compress: true},
	)
	// The same Interceptor used by the caller worker. On the client side its
	// ClientOutboundInterceptor.ExecuteWorkflow stamps the keyID from
	// CryptContext on the Go ctx into the workflow's start headers, replacing
	// what a workflow.ContextPropagator would otherwise do.
	clientOptions.Interceptors = []interceptor.ClientInterceptor{
		&nexusperendpointencryption.Interceptor{EndpointKeys: nexusperendpointencryption.EndpointKeys},
	}

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	// By default exercise both endpoints in sequence so a single starter run
	// demonstrates the per-endpoint key variation end-to-end. Pass
	// -endpoint endpoint-a or -endpoint endpoint-b to run just one.
	targets := []string{"endpoint-a", "endpoint-b"}
	if endpointFlag != "" {
		targets = []string{endpointFlag}
	}

	for _, endpoint := range targets {
		runOne(c, endpoint, nameFlag)
	}
}

// runOne pre-seeds CryptContext on the Go context BEFORE calling
// ExecuteWorkflow so that the caller workflow's own input and result payloads
// (event #1 onward) are encrypted under the per-endpoint keyID. Without this
// the workflow's outbound interceptor would still encrypt the Nexus call
// payloads correctly, but the workflow's input/result would fall back to an
// unencrypted state.
func runOne(c client.Client, endpoint, name string) {
	keyID, ok := nexusperendpointencryption.EndpointKeys[endpoint]
	if !ok {
		log.Fatalf("unknown endpoint %q -- no keyID configured", endpoint)
	}

	ctx := context.WithValue(context.Background(), nexusperendpointencryption.PropagateKey, nexusperendpointencryption.CryptContext{KeyID: keyID})

	workflowOptions := client.StartWorkflowOptions{
		ID:        "nexus-per-endpoint-encryption_" + endpoint + "_" + time.Now().Format("20060102150405.000000"),
		TaskQueue: caller.TaskQueue,
	}

	wr, err := c.ExecuteWorkflow(ctx, workflowOptions, caller.HelloCallerWorkflow, endpoint, name)
	if err != nil {
		log.Fatalln("Unable to execute workflow", err)
	}
	log.Println("Started workflow", "Endpoint", endpoint, "KeyID", keyID, "WorkflowID", wr.GetID(), "RunID", wr.GetRunID())

	var result string
	if err := wr.Get(context.Background(), &result); err != nil {
		log.Fatalln("Unable to get workflow result", err)
	}
	log.Println("Workflow result", "Endpoint", endpoint, "Result", result)
}
