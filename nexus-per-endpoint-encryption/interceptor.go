package nexusperendpointencryption

import (
	"context"
	"reflect"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

// EndpointAware carries the target Nexus endpoint alongside a sync operation
// result Value. The handler-side inbound interceptor produces this wrap; the
// encrypting DataConverter unwraps it at ToPayload time and uses the endpoint
// to select the per-endpoint key. This is the fallback for the sync response
// path, which does not flow through any workflow header the codec could read.
type EndpointAware struct {
	Value    any
	Endpoint string
}

// contextKey is the unexported type used for stashing CryptContext on Go and
// workflow contexts.
type contextKey struct{}

// PropagateKey identifies the CryptContext value on a context.Context or
// workflow.Context.
var PropagateKey = contextKey{}

// CryptContext carries the active encryption keyID through Go and workflow
// contexts. The ContextAware DataConverter reads it to pick the right key
// when encoding payloads. Endpoint travels alongside KeyID so the workflow
// inbound interceptor can wrap Query and Update results in EndpointAware
// without having to reverse-lookup the endpoint from the key.
type CryptContext struct {
	KeyID    string `json:"keyId"`
	Endpoint string `json:"endpoint,omitempty"`
}

// nexusEndpointHeader is the Nexus operation header that carries the target
// endpoint name from caller to handler. The caller-side outbound interceptor
// stamps it; the handler-side inbound interceptor reads it. This replaces
// the workflow.ContextPropagator path -- no JSON-encoded CryptContext on the
// workflow-start header, just a plain string on the Nexus boundary.
const nexusEndpointHeader = "encryption-endpoint"

// Interceptor wires per-endpoint key resolution into the worker and client.
// The caller-side outbound stamps the target endpoint into call headers
// (Nexus header for Nexus calls; workflow header for ExecuteWorkflow / Query
// / Update). The handler-side inbound reads the appropriate header, then
// wraps results in EndpointAware so the DataConverter encodes under the
// per-endpoint key.
type Interceptor struct {
	interceptor.InterceptorBase
	// EndpointKeys maps Nexus endpoint name -> keyID. Required. Must be
	// configured identically on caller and handler workers.
	EndpointKeys map[string]string
}

// --- interceptor.ClientInterceptor ---

// InterceptClient implements interceptor.ClientInterceptor.
func (i *Interceptor) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	out := &clientOutbound{parent: i}
	out.Next = next
	return out
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

// --- client outbound: stamp endpoint into call headers ---

type clientOutbound struct {
	interceptor.ClientOutboundInterceptorBase
	parent *Interceptor
}

// ExecuteWorkflow stamps the endpoint into the workflow's start header.
// On the handler side this is what carries the endpoint to handler workflows
// started via Nexus workflow-run operations: the Nexus inbound seeds
// CryptContext on the Go ctx, this method reads it back out and writes the
// workflow start header.
func (c *clientOutbound) ExecuteWorkflow(ctx context.Context, in *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
	stampEndpointHeader(ctx)
	return c.Next.ExecuteWorkflow(ctx, in)
}

// QueryWorkflow stamps the endpoint into the query header.
func (c *clientOutbound) QueryWorkflow(ctx context.Context, in *interceptor.ClientQueryWorkflowInput) (converter.EncodedValue, error) {
	stampEndpointHeader(ctx)
	return c.Next.QueryWorkflow(ctx, in)
}

// UpdateWorkflow stamps the endpoint into the update header.
func (c *clientOutbound) UpdateWorkflow(ctx context.Context, in *interceptor.ClientUpdateWorkflowInput) (client.WorkflowUpdateHandle, error) {
	stampEndpointHeader(ctx)
	return c.Next.UpdateWorkflow(ctx, in)
}

// SignalWorkflow stamps the endpoint into the signal header so the workflow
// inbound interceptor can recover it inside HandleSignal if needed.
func (c *clientOutbound) SignalWorkflow(ctx context.Context, in *interceptor.ClientSignalWorkflowInput) error {
	stampEndpointHeader(ctx)
	return c.Next.SignalWorkflow(ctx, in)
}

// stampEndpointHeader writes the endpoint on ctx (via CryptContext) into the
// per-call header map exposed by interceptor.Header. Caller-side outbound
// interceptors that run before headerPropagated picks up the result.
func stampEndpointHeader(ctx context.Context) {
	v, ok := ctx.Value(PropagateKey).(CryptContext)
	if !ok || v.Endpoint == "" {
		return
	}
	h := interceptor.Header(ctx)
	if h == nil {
		return
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(v.Endpoint)
	if err != nil {
		return
	}
	h[nexusEndpointHeader] = payload
}

// endpointFromWorkflowHeader returns the endpoint set on the workflow's
// current header (the workflow-start header for ExecuteWorkflow, the
// per-call header for HandleQuery / ExecuteUpdate / HandleSignal). Returns
// "" if the header is absent.
func endpointFromWorkflowHeader(ctx workflow.Context) string {
	h := interceptor.WorkflowHeader(ctx)
	if h == nil {
		return ""
	}
	payload, ok := h[nexusEndpointHeader]
	if !ok {
		return ""
	}
	var endpoint string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &endpoint); err != nil {
		return ""
	}
	return endpoint
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

// HandleQuery wraps the query result in EndpointAware when the per-call
// query header carries the endpoint stamped by the caller-side
// clientOutbound. The data converter pulls the endpoint back out at
// ToPayloads time to encrypt under the per-endpoint key.
func (in *workflowInboundInterceptor) HandleQuery(ctx workflow.Context, input *interceptor.HandleQueryInput) (any, error) {
	result, err := in.Next.HandleQuery(ctx, input)
	if err != nil {
		return result, err
	}
	endpoint := endpointFromWorkflowHeader(ctx)
	if endpoint == "" {
		return result, nil
	}
	return EndpointAware{Value: result, Endpoint: endpoint}, nil
}

// ExecuteUpdate wraps the update result in EndpointAware for the same reason
// HandleQuery does. ValidateUpdate is not wrapped because it returns no value.
func (in *workflowInboundInterceptor) ExecuteUpdate(ctx workflow.Context, input *interceptor.UpdateInput) (any, error) {
	result, err := in.Next.ExecuteUpdate(ctx, input)
	if err != nil {
		return result, err
	}
	endpoint := endpointFromWorkflowHeader(ctx)
	if endpoint == "" {
		return result, nil
	}
	return EndpointAware{Value: result, Endpoint: endpoint}, nil
}

type workflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	parent *Interceptor
}

// ExecuteNexusOperation looks up the keyID for the target Nexus endpoint,
// attaches CryptContext to a derived workflow context so the operation
// input encodes under the per-endpoint key, and stamps the endpoint into
// the Nexus header so the handler-side inbound interceptor can recover it
// without depending on temporalnexus.GetOperationInfo.
func (out *workflowOutboundInterceptor) ExecuteNexusOperation(
	ctx workflow.Context,
	input interceptor.ExecuteNexusOperationInput,
) workflow.NexusOperationFuture {
	endpoint := input.Client.Endpoint()
	if keyID, ok := out.parent.EndpointKeys[endpoint]; ok && keyID != "" {
		ctx = workflow.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID, Endpoint: endpoint})
		if input.NexusHeader == nil {
			input.NexusHeader = nexus.Header{}
		}
		input.NexusHeader.Set(nexusEndpointHeader, endpoint)
	}
	return out.Next.ExecuteNexusOperation(ctx, input)
}

type nexusOperationInboundInterceptor struct {
	interceptor.NexusOperationInboundInterceptorBase
	parent *Interceptor
}

// StartOperation reads the inbound endpoint from the Nexus operation header
// that the caller-side outbound interceptor stamped. The handler-side
// inbound then sets CryptContext on the Go ctx (so any downstream handler
// code can read the active keyID) and wraps the sync result in
// EndpointAware so the DataConverter can encrypt under the per-endpoint key.
func (n *nexusOperationInboundInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	endpoint := input.Options.Header.Get(nexusEndpointHeader)
	keyID, ok := n.parent.EndpointKeys[endpoint]
	if !ok || keyID == "" {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "no encryption key configured for endpoint %q", endpoint)
	}
	ctx = context.WithValue(ctx, PropagateKey, CryptContext{KeyID: keyID, Endpoint: endpoint})
	result, err := n.Next.StartOperation(ctx, input)
	if err != nil || result == nil {
		return result, err
	}
	if _, async := result.(*nexus.HandlerStartOperationResultAsync); async {
		return result, nil
	}
	// Sync result: *nexus.HandlerStartOperationResultSync[T] is generic, so
	// reach Value via reflection.
	valueField := reflect.ValueOf(result).Elem().FieldByName("Value")
	if !valueField.IsValid() {
		return result, nil
	}
	return &nexus.HandlerStartOperationResultSync[any]{
		Value: EndpointAware{Value: valueField.Interface(), Endpoint: endpoint},
	}, nil
}
