package nexusperendpointencryption

import (
	"context"
	"fmt"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
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
	KeyID string
}

// headerKey is the Temporal header name used to ferry the keyID across the
// workflow-start boundary. The client-side interceptor writes it; the worker-
// side interceptor reads it back. This is the work a ContextPropagator would
// otherwise do -- here it lives in the interceptors so there's a single place
// to look.
const headerKey = "encryption-key-id"

// Interceptor implements both interceptor.ClientInterceptor and
// interceptor.WorkerInterceptor. Wire the same instance into:
//
//   - client.Options.Interceptors (so workflow-start calls inject the keyID
//     header from CryptContext on the Go ctx)
//   - worker.Options.Interceptors (so workflow execution reads the header
//     back, and Nexus boundary calls swap in the per-endpoint key)
//
// No workflow.ContextPropagator is needed: interceptor.Header and
// interceptor.WorkflowHeader give interceptors direct read/write access to
// workflow headers.
type Interceptor struct {
	interceptor.InterceptorBase
	// EndpointKeys maps Nexus endpoint name -> keyID. Required. Must be
	// configured identically on caller and handler workers.
	EndpointKeys map[string]string
}

// --- ClientInterceptor ---

// InterceptClient implements interceptor.ClientInterceptor.
func (i *Interceptor) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	out := &clientOutboundInterceptor{parent: i}
	out.Next = next
	return out
}

type clientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
	parent *Interceptor
}

// ExecuteWorkflow injects the active keyID (from CryptContext on the Go ctx)
// into the workflow's start headers. The corresponding WorkflowInboundInterceptor
// reads it back into CryptContext on the workflow context.
func (c *clientOutboundInterceptor) ExecuteWorkflow(ctx context.Context, in *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
	if err := writeKeyIDHeader(ctx, interceptor.Header(ctx)); err != nil {
		return nil, err
	}
	return c.Next.ExecuteWorkflow(ctx, in)
}

// SignalWithStartWorkflow propagates CryptContext on the signal-with-start
// path, same shape as ExecuteWorkflow.
func (c *clientOutboundInterceptor) SignalWithStartWorkflow(ctx context.Context, in *interceptor.ClientSignalWithStartWorkflowInput) (client.WorkflowRun, error) {
	if err := writeKeyIDHeader(ctx, interceptor.Header(ctx)); err != nil {
		return nil, err
	}
	return c.Next.SignalWithStartWorkflow(ctx, in)
}

// --- WorkerInterceptor ---

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

// ExecuteWorkflow reads the keyID header written by the client-side interceptor
// and seeds CryptContext on the workflow.Context so the ContextAware
// DataConverter encrypts at-rest payloads with the per-endpoint key.
func (in *workflowInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, input *interceptor.ExecuteWorkflowInput) (interface{}, error) {
	if keyID := readKeyIDHeader(interceptor.WorkflowHeader(ctx)); keyID != "" {
		ctx = workflow.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID})
	}
	return in.Next.ExecuteWorkflow(ctx, input)
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
// flows through the ClientInterceptor above, stamping the keyID onto the
// handler workflow's start headers.
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

// --- header helpers ---

// writeKeyIDHeader encodes the CryptContext's keyID from ctx into the workflow
// header map (returned by interceptor.Header / interceptor.WorkflowHeader).
// No-op if there's no CryptContext on ctx or no header map is available.
func writeKeyIDHeader(ctx context.Context, header map[string]*commonpb.Payload) error {
	if header == nil {
		return nil
	}
	v, ok := ctx.Value(PropagateKey).(CryptContext)
	if !ok || v.KeyID == "" {
		return nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(v.KeyID)
	if err != nil {
		return fmt.Errorf("encoding %s header: %w", headerKey, err)
	}
	header[headerKey] = payload
	return nil
}

// readKeyIDHeader extracts the keyID written by writeKeyIDHeader, or returns
// "" if the header is absent or unparseable.
func readKeyIDHeader(header map[string]*commonpb.Payload) string {
	if header == nil {
		return ""
	}
	payload, ok := header[headerKey]
	if !ok {
		return ""
	}
	var keyID string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &keyID); err != nil {
		return ""
	}
	return keyID
}
