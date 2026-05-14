package nexusperendpointencryption

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"
)

// contextKey is the unexported type used for stashing CryptContext on Go and
// workflow contexts.
type contextKey struct{}

// PropagateKey identifies the CryptContext value on a context.Context or
// workflow.Context.
var PropagateKey = contextKey{}

// CryptContext carries the active encryption keyID through Go and workflow
// contexts. The ContextAware DataConverter reads it to pick the right key
// when encoding payloads.
type CryptContext struct {
	KeyID string `json:"keyId"`
}

// headerKey is the workflow header key the propagator uses to ferry the
// CryptContext across the workflow-start boundary.
const headerKey = "encryption-key-id"

// Interceptor wires per-endpoint key resolution into the worker and bridges
// CryptContext across workflow boundaries. The same instance is registered as
// both a workflow.ContextPropagator (so workflow start headers carry the
// keyID, which lands on the workflow's root context where result encoding
// can see it) and an interceptor.WorkerInterceptor (so Nexus boundary calls
// swap in the per-endpoint key, and the handler-side inbound seeds the
// keyID into the Go context before NewWorkflowRunOperation runs).
type Interceptor struct {
	interceptor.WorkerInterceptorBase
	// EndpointKeys maps Nexus endpoint name -> keyID. Required. Must be
	// configured identically on caller and handler workers.
	EndpointKeys map[string]string
}

// --- workflow.ContextPropagator ---

// Inject implements workflow.ContextPropagator. Called by the client on
// workflow-start to copy CryptContext from the Go ctx into the workflow's
// start header.
func (i *Interceptor) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	v, ok := ctx.Value(PropagateKey).(CryptContext)
	if !ok || v.KeyID == "" {
		return nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(v)
	if err != nil {
		return err
	}
	writer.Set(headerKey, payload)
	return nil
}

// InjectFromWorkflow implements workflow.ContextPropagator. Called when a
// workflow starts a child workflow or schedules an activity. The
// CryptContext on the parent workflow.Context flows into the child/activity
// headers.
func (i *Interceptor) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	v, ok := ctx.Value(PropagateKey).(CryptContext)
	if !ok || v.KeyID == "" {
		return nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(v)
	if err != nil {
		return err
	}
	writer.Set(headerKey, payload)
	return nil
}

// Extract implements workflow.ContextPropagator. Called by the worker as part
// of activity setup to copy the keyID header back into the Go ctx.
func (i *Interceptor) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	if payload, ok := reader.Get(headerKey); ok {
		var v CryptContext
		if err := converter.GetDefaultDataConverter().FromPayload(payload, &v); err == nil && v.KeyID != "" {
			ctx = context.WithValue(ctx, PropagateKey, v)
		}
	}
	return ctx, nil
}

// ExtractToWorkflow implements workflow.ContextPropagator. Called by the
// worker BEFORE workflowExecutor.Execute computes the data converter from
// the root context. That ordering is what lets the workflow result be
// encoded under the per-endpoint key.
func (i *Interceptor) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if payload, ok := reader.Get(headerKey); ok {
		var v CryptContext
		if err := converter.GetDefaultDataConverter().FromPayload(payload, &v); err == nil && v.KeyID != "" {
			ctx = workflow.WithValue(ctx, PropagateKey, v)
		}
	}
	return ctx, nil
}

// --- interceptor.WorkerInterceptor ---

// InterceptWorkflow implements interceptor.WorkerInterceptor.
func (i *Interceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	in := &workflowInboundInterceptor{parent: i}
	in.Next = next
	return in
}

// InterceptNexusOperation implements interceptor.WorkerInterceptor.
func (i *Interceptor) InterceptNexusOperation(ctx context.Context, next interceptor.NexusOperationInboundInterceptor) interceptor.NexusOperationInboundInterceptor {
	in := &nexusOperationInboundInterceptor{parent: i}
	in.Next = next
	return in
}

type workflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	parent *Interceptor
}

func (in *workflowInboundInterceptor) Init(next interceptor.WorkflowOutboundInterceptor) error {
	out := &workflowOutboundInterceptor{parent: in.parent}
	out.Next = next
	return in.Next.Init(out)
}

type workflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	parent *Interceptor
}

// ExecuteNexusOperation looks up the keyID for the target Nexus endpoint and
// attaches CryptContext to a derived workflow context for the duration of the
// Next.ExecuteNexusOperation call. Scope is just this operation; it does not
// leak into the rest of the workflow.
func (out *workflowOutboundInterceptor) ExecuteNexusOperation(
	ctx workflow.Context,
	input interceptor.ExecuteNexusOperationInput,
) workflow.NexusOperationFuture {
	endpoint := input.Client.Endpoint()
	if keyID, ok := out.parent.EndpointKeys[endpoint]; ok && keyID != "" {
		ctx = workflow.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID})
	}
	return out.Next.ExecuteNexusOperation(ctx, input)
}

type nexusOperationInboundInterceptor struct {
	interceptor.NexusOperationInboundInterceptorBase
	parent *Interceptor
}

// StartOperation reads the inbound endpoint via temporalnexus.GetOperationInfo
// and attaches CryptContext to the Go context. Any client.ExecuteWorkflow the
// handler makes (typically via temporalnexus.NewWorkflowRunOperation) then
// flows through this Interceptor's ContextPropagator.Inject, stamping the
// keyID onto the handler workflow's start headers.
//
// Requires Temporal server >= 1.30.0 for GetOperationInfo().Endpoint.
func (n *nexusOperationInboundInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	endpoint := temporalnexus.GetOperationInfo(ctx).Endpoint
	keyID, ok := n.parent.EndpointKeys[endpoint]
	if !ok || keyID == "" {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "no encryption key configured for endpoint %q", endpoint)
	}
	ctx = context.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID})
	return n.Next.StartOperation(ctx, input)
}
