package main

import (
	"context"
	"log"
	"time"

	"github.com/temporalio/samples-go/update"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func createWorker(c client.Client, taskQueue string) worker.Worker {
	w := worker.New(c, taskQueue, worker.Options{
		WorkflowPanicPolicy: worker.FailWorkflow,
	})

	w.RegisterWorkflow(update.SpeculativeWorkflowNonDeterminismRepo)

	return w
}

func main() {
	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := createWorker(c, "update")
	w.Start()

	workflowOptions := client.StartWorkflowOptions{
		ID:        "update-workflow-ID",
		TaskQueue: "update",
	}

	we, err := c.ExecuteWorkflow(context.Background(), workflowOptions, update.SpeculativeWorkflowNonDeterminismRepo)
	if err != nil {
		log.Fatalln("Unable to execute workflow", err)
	}

	log.Println("Started workflow", "WorkflowID", we.GetID(), "RunID", we.GetRunID())

	log.Println("Sending updates to be rejected to trigger a speculative workflow task")
	for i := 0; i < 3; i++ {
		handle, err := c.UpdateWorkflow(context.Background(), we.GetID(), we.GetRunID(), "update", true)
		if err != nil {
			log.Fatal("error issuing update request", err)
		}
		err = handle.Get(context.Background(), nil)
		log.Printf("Update failed with: %v", err)
	}
	log.Println("Sending signal to advance the workflow")
	if err = c.SignalWorkflow(context.Background(), we.GetID(), we.GetRunID(), update.Done, nil); err != nil {
		log.Fatalf("failed to send %q signal to workflow: %v", update.Done, err)
	}
	log.Println("Waiting 10s to allow the signal to trigger a workflow task")
	time.Sleep(10 * time.Second)

	log.Println("Restarting worker")
	w.Stop()
	w = createWorker(c, "update")
	w.Start()

	log.Println("Sending signal to trigger replay")
	if err = c.SignalWorkflow(context.Background(), we.GetID(), we.GetRunID(), update.Done, nil); err != nil {
		log.Fatalf("failed to send %q signal to workflow: %v", update.Done, err)
	}
	var wfresult int
	if err = we.Get(context.Background(), &wfresult); err != nil {
		log.Fatalf("unable get workflow result: %v", err)
	}
	log.Println("workflow result:", wfresult)
}
