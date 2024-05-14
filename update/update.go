package update

import (
	"errors"
	"math/rand"

	"go.temporal.io/sdk/workflow"
)

const (
	FetchAndAdd = "fetch_and_add"
	Done        = "done"
)

func SpeculativeWorkflowNonDeterminismRepo(ctx workflow.Context) (int, error) {
	log := workflow.GetLogger(ctx)
	log.Info("Workflow started")
	counter := 0
	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		"update",
		func(ctx workflow.Context, reject bool) (bool, error) {
			counter++
			return reject, nil
		},
		workflow.UpdateHandlerOptions{Validator: validator},
	); err != nil {
		return 0, err
	}
	log.Info("Waiting on workflow length")
	workflow.Await(ctx, func() bool {
		return workflow.GetInfo(ctx).GetCurrentHistoryLength() == 6
	})
	log.Info("Waiting on signal channel")
	_ = workflow.GetSignalChannel(ctx, Done).Receive(ctx, nil)
	// Generate some commands
	encodedRandom := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		//workflowcheck:ignore
		return rand.Intn(10)
	})
	var random int
	err := encodedRandom.Get(&random)
	if err != nil {
		return 0, err
	}
	log.Info("Waiting on signal channel for a second time")
	_ = workflow.GetSignalChannel(ctx, Done).Receive(ctx, nil)
	return counter, ctx.Err()
}

func validator(ctx workflow.Context, reject bool) error {
	log := workflow.GetLogger(ctx)
	if reject {
		log.Debug("Rejecting update")
		return errors.New("rejecting update")
	}
	log.Debug("Accepting update")
	return nil
}
