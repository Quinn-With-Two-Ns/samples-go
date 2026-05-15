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

func runOne(c client.Client, endpoint, name string) {
	workflowOptions := client.StartWorkflowOptions{
		ID:        "nexus-per-endpoint-encryption_" + endpoint + "_" + time.Now().Format("20060102150405.000000"),
		TaskQueue: caller.TaskQueue,
	}

	wr, err := c.ExecuteWorkflow(context.Background(), workflowOptions, caller.HelloCallerWorkflow, endpoint, name)
	if err != nil {
		log.Fatalln("Unable to execute workflow", err)
	}
	log.Println("Started workflow", "Endpoint", endpoint, "WorkflowID", wr.GetID(), "RunID", wr.GetRunID())

	var result string
	if err := wr.Get(context.Background(), &result); err != nil {
		log.Fatalln("Unable to get workflow result", err)
	}
	log.Println("Workflow result", "Endpoint", endpoint, "Result", result)
}
