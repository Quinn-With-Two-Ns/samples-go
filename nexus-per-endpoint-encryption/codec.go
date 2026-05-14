package nexusperendpointencryption

import (
	"context"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

const (
	// MetadataEncodingEncrypted is "binary/encrypted".
	MetadataEncodingEncrypted = "binary/encrypted"

	// MetadataEncryptionKeyID is "encryption-key-id".
	MetadataEncryptionKeyID = "encryption-key-id"
)

// DataConverterOptions configures the encrypting DataConverter. The
// endpoint->keyID map intentionally does NOT live here -- the codec receives
// its keyID exclusively from CryptContext on the workflow/Go context. The
// EndpointKeys map lives on WorkerInterceptor and in the starter wiring.
type DataConverterOptions struct {
	// Compress enables ZLib compression before encryption. Compression must
	// happen before encryption because encrypted output does not compress
	// well.
	Compress bool
}

// Codec implements converter.PayloadCodec using AES-GCM. It also implements
// WorkflowContextAwarePayloadCodec so that ContextAwareCodecDataConverter can
// swap in a per-keyID variant from CryptContext on the supplied context.
// Decode reads MetadataEncryptionKeyID from each payload and looks up the
// key via getKey, so a Codec{} with an empty KeyID still decodes payloads
// correctly -- as the codec-server depends on.
type Codec struct {
	KeyID string
}

// NewEncryptingDataConverter wires the AES-GCM Codec (and optional ZLib
// compression) into a ContextAwareCodecDataConverter. The active encryption
// keyID is read from CryptContext on the workflow/Go context at encode time.
func NewEncryptingDataConverter(parent converter.DataConverter, options DataConverterOptions) *ContextAwareCodecDataConverter {
	codecs := []converter.PayloadCodec{&Codec{}}
	if options.Compress {
		// Compression must run before encryption (codecs apply last -> first).
		codecs = append(codecs, converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	}
	return NewContextAwareCodecDataConverter(parent, codecs...)
}

// WithWorkflowContext implements WorkflowContextAwarePayloadCodec.
func (e *Codec) WithWorkflowContext(ctx workflow.Context) converter.PayloadCodec {
	return e.withKeyIDFromContext(ctx)
}

// WithContext implements WorkflowContextAwarePayloadCodec.
func (e *Codec) WithContext(ctx context.Context) converter.PayloadCodec {
	return e.withKeyIDFromContext(ctx)
}

// withKeyIDFromContext returns a Codec keyed by CryptContext's KeyID if one is
// present on ctx and different from the current KeyID. Returning the receiver
// unchanged when nothing changes lets ContextAwareCodecDataConverter skip the
// per-call allocation.
func (e *Codec) withKeyIDFromContext(ctx interface{ Value(key interface{}) interface{} }) converter.PayloadCodec {
	v, ok := ctx.Value(PropagateKey).(CryptContext)
	if !ok || v.KeyID == e.KeyID {
		return e
	}
	return &Codec{KeyID: v.KeyID}
}

// Encode implements converter.PayloadCodec.Encode. It writes the active KeyID
// into MetadataEncryptionKeyID so Decode (and the codec server) know which key
// to use.
func (e *Codec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		origBytes, err := p.Marshal()
		if err != nil {
			return payloads, err
		}

		key, err := getKey(e.KeyID)
		if err != nil {
			return payloads, err
		}

		b, err := encrypt(origBytes, key)
		if err != nil {
			return payloads, err
		}

		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				converter.MetadataEncoding: []byte(MetadataEncodingEncrypted),
				MetadataEncryptionKeyID:    []byte(e.KeyID),
			},
			Data: b,
		}
	}

	return result, nil
}

// Decode implements converter.PayloadCodec.Decode. It reads the per-payload
// MetadataEncryptionKeyID and looks up the matching key. This is what allows a
// single Codec{} instance in the codec-server to decode payloads produced
// under either endpoint's keyID.
func (e *Codec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		// Only if it's encrypted
		if string(p.Metadata[converter.MetadataEncoding]) != MetadataEncodingEncrypted {
			result[i] = p
			continue
		}

		// MetadataEncryptionKeyID is not covered by AES-GCM authentication, which only
		// authenticates p.Data. Tampering with the keyID causes decryption to fail with
		// an auth tag mismatch rather than silent plaintext exposure.
		keyID, ok := p.Metadata[MetadataEncryptionKeyID]
		if !ok {
			return payloads, fmt.Errorf("no encryption key id")
		}

		key, err := getKey(string(keyID))
		if err != nil {
			return payloads, err
		}

		b, err := decrypt(p.Data, key)
		if err != nil {
			return payloads, err
		}

		result[i] = &commonpb.Payload{}
		err = result[i].Unmarshal(b)
		if err != nil {
			return payloads, err
		}
	}

	return result, nil
}
