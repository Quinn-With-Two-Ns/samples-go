package nexusperendpointencryption

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"
)

// WorkerInterceptor wires per-endpoint key resolution into both the caller
// (workflow outbound) and handler (Nexus operation inbound) sides. The same
// instance type is registered on both workers and uses the same EndpointKeys
// map.
type WorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
	// EndpointKeys maps Nexus endpoint name -> keyID. Required. Must be
	// configured identically on caller and handler workers.
	EndpointKeys map[string]string
}

func (w *WorkerInterceptor) InterceptWorkflow(
	ctx workflow.Context, next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	in := &workflowInboundInterceptor{parent: w}
	in.Next = next
	return in
}

func (w *WorkerInterceptor) InterceptNexusOperation(
	ctx context.Context, next interceptor.NexusOperationInboundInterceptor,
) interceptor.NexusOperationInboundInterceptor {
	i := &nexusOperationInboundInterceptor{parent: w}
	i.Next = next
	return i
}

type workflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	parent *WorkerInterceptor
}

func (in *workflowInboundInterceptor) Init(next interceptor.WorkflowOutboundInterceptor) error {
	out := &workflowOutboundInterceptor{parent: in.parent}
	out.Next = next
	return in.Next.Init(out)
}

type workflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	parent *WorkerInterceptor
}

// ExecuteNexusOperation looks up the keyID for the target Nexus endpoint and
// attaches a CryptContext to a derived workflow context for the duration of
// the Next.ExecuteNexusOperation call. The derived context is scoped to just
// this Nexus operation invocation and does not leak into the rest of the
// workflow.
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
	parent *WorkerInterceptor
}

// StartOperation reads the inbound endpoint via temporalnexus.GetOperationInfo
// and attaches CryptContext to the Go context. The handler client's
// ContextPropagator then carries the CryptContext into the handler-started
// workflow.
//
// Requires Temporal server >= 1.30.0 for GetOperationInfo().Endpoint to be
// populated. On older servers see the README appendix for the fallback that
// piggybacks the keyID on a Nexus header.
func (n *nexusOperationInboundInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	endpoint := temporalnexus.GetOperationInfo(ctx).Endpoint
	keyID, ok := n.parent.EndpointKeys[endpoint]
	if !ok || keyID == "" {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "no encryption key configured for endpoint %q", endpoint)
	}
	ctx = context.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID})
	return n.Next.StartOperation(ctx, input)
}
