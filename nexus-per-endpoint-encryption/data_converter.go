package nexusperendpointencryption

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// WorkflowContextAwarePayloadCodec is implemented by PayloadCodecs that vary
// their behaviour based on the current workflow.Context or Go context.
// ContextAwareCodecDataConverter asks each codec in the chain whether it
// implements this interface and swaps in the per-context variant; codecs that
// don't care about context (e.g. compression) don't implement it and pass
// through unchanged.
//
// This mirrors the SDK's converter.PayloadCodecWithSerializationContext
// pattern, but at the workflow.ContextAware layer.
type WorkflowContextAwarePayloadCodec interface {
	converter.PayloadCodec
	WithWorkflowContext(workflow.Context) converter.PayloadCodec
	WithContext(context.Context) converter.PayloadCodec
}

// ContextAwareCodecDataConverter is the workflow.ContextAware sibling of
// converter.CodecDataConverter. It wraps a parent DataConverter with a list of
// PayloadCodecs and exposes WithWorkflowContext / WithContext that re-derive
// any WorkflowContextAwarePayloadCodec codecs from the supplied context.
// Plain PayloadCodecs are reused as-is.
//
// When converter.CodecDataConverter itself implements workflow.ContextAware
// this type can go away.
type ContextAwareCodecDataConverter struct {
	converter.DataConverter // converter.NewCodecDataConverter(parent, codecs...)
	parent                  converter.DataConverter
	codecs                  []converter.PayloadCodec
}

// NewContextAwareCodecDataConverter wraps parent with a CodecDataConverter
// chain. Any codec that implements WorkflowContextAwarePayloadCodec will be
// re-derived per call on WithWorkflowContext / WithContext.
func NewContextAwareCodecDataConverter(parent converter.DataConverter, codecs ...converter.PayloadCodec) *ContextAwareCodecDataConverter {
	return &ContextAwareCodecDataConverter{
		DataConverter: converter.NewCodecDataConverter(parent, codecs...),
		parent:        parent,
		codecs:        codecs,
	}
}

// WithWorkflowContext implements workflow.ContextAware.
func (dc *ContextAwareCodecDataConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	parent := dc.parent
	if pcw, ok := parent.(workflow.ContextAware); ok {
		parent = pcw.WithWorkflowContext(ctx)
	}
	newCodecs := make([]converter.PayloadCodec, len(dc.codecs))
	changed := parent != dc.parent
	for i, c := range dc.codecs {
		if cw, ok := c.(WorkflowContextAwarePayloadCodec); ok {
			newCodecs[i] = cw.WithWorkflowContext(ctx)
			changed = changed || newCodecs[i] != c
		} else {
			newCodecs[i] = c
		}
	}
	if !changed {
		return dc
	}
	return &ContextAwareCodecDataConverter{
		DataConverter: converter.NewCodecDataConverter(parent, newCodecs...),
		parent:        parent,
		codecs:        newCodecs,
	}
}

// ToPayload overrides the embedded DataConverter to recognise EndpointAware
// values. When the inbound Nexus interceptor wraps a sync result, this method
// pulls the endpoint out, derives a CryptContext-bearing context, and
// re-routes the encode through the per-endpoint codec chain. Plain values
// pass through unchanged, so the codec's CryptContext-on-context path keeps
// working for the workflow encoding paths.
func (dc *ContextAwareCodecDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	ea, ok := value.(EndpointAware)
	if !ok {
		return dc.DataConverter.ToPayload(value)
	}
	return dc.forEndpoint(ea.Endpoint).ToPayload(ea.Value)
}

// ToPayloads recognises a lone EndpointAware value the same way ToPayload
// does. The workflow Query and Update encoding paths in the SDK route
// through ToPayloads (single-arg variadic), so this override is what makes
// the workflow inbound interceptor's wrap effective for those responses.
func (dc *ContextAwareCodecDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	if len(values) == 1 {
		if ea, ok := values[0].(EndpointAware); ok {
			return dc.forEndpoint(ea.Endpoint).ToPayloads(ea.Value)
		}
	}
	return dc.DataConverter.ToPayloads(values...)
}

// forEndpoint returns the DataConverter view for encoding under the named
// endpoint's key. If the endpoint is unknown, the plain embedded converter
// (no per-endpoint key) is returned.
func (dc *ContextAwareCodecDataConverter) forEndpoint(endpoint string) converter.DataConverter {
	keyID := EndpointKeys[endpoint]
	if keyID == "" {
		return dc.DataConverter
	}
	return dc.WithContext(context.WithValue(context.Background(), PropagateKey, CryptContext{KeyID: keyID, Endpoint: endpoint}))
}

// WithContext implements workflow.ContextAware.
func (dc *ContextAwareCodecDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	parent := dc.parent
	if pcw, ok := parent.(workflow.ContextAware); ok {
		parent = pcw.WithContext(ctx)
	}
	newCodecs := make([]converter.PayloadCodec, len(dc.codecs))
	changed := parent != dc.parent
	for i, c := range dc.codecs {
		if cw, ok := c.(WorkflowContextAwarePayloadCodec); ok {
			newCodecs[i] = cw.WithContext(ctx)
			changed = changed || newCodecs[i] != c
		} else {
			newCodecs[i] = c
		}
	}
	if !changed {
		return dc
	}
	return &ContextAwareCodecDataConverter{
		DataConverter: converter.NewCodecDataConverter(parent, newCodecs...),
		parent:        parent,
		codecs:        newCodecs,
	}
}
