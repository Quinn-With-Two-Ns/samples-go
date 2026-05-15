package nexusperendpointencryption

import (
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

const (
	// MetadataEncodingEncrypted is "binary/encrypted".
	MetadataEncodingEncrypted = "binary/encrypted"

	// MetadataEncryptionKeyID is "encryption-key-id".
	MetadataEncryptionKeyID = "encryption-key-id"
)

// EndpointKeys maps Nexus endpoint name -> keyID. Both workers configure the
// same map so the keyID derived on the caller side and on the handler side
// agree. In production each worker process would resolve per-endpoint keys
// from a KMS independently; only the keyID metadata flows over the wire and
// on the payload, never the key material.
var EndpointKeys = map[string]string{
	"endpoint-a": "key-a",
	"endpoint-b": "key-b",
}

// Codec implements converter.PayloadCodec using AES-GCM. It also implements
// converter.PayloadCodecWithSerializationContext, so the SDK calls
// WithSerializationContext before every serialization at a Nexus boundary --
// the caller's ExecuteNexusOperation, the handler's Nexus task, and the
// spawned workflow's input/output for workflow-run operations -- handing the
// codec the target endpoint. The codec turns endpoint into keyID via
// EndpointKeys and encrypts under that key.
//
// At non-Nexus boundaries (Workflow / Activity contexts, or no context at all)
// KeyID stays empty and Encode passes payloads through unencrypted. Decode
// reads MetadataEncryptionKeyID from each payload and looks up the key, so a
// Codec{} with empty KeyID still decodes correctly -- as the codec-server
// depends on.
type Codec struct {
	KeyID string
}

// WithSerializationContext implements
// converter.PayloadCodecWithSerializationContext. Only NexusSerializationContext
// produces a keyed codec; other contexts (Workflow, Activity) leave the codec
// in pass-through mode.
func (e *Codec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	nsc, ok := ctx.(converter.NexusSerializationContext)
	if !ok {
		return e
	}
	keyID := EndpointKeys[nsc.Endpoint]
	if keyID == e.KeyID {
		return e
	}
	return &Codec{KeyID: keyID}
}

// Encode implements converter.PayloadCodec.Encode. It writes the active KeyID
// into MetadataEncryptionKeyID so Decode (and the codec server) know which
// key to use. If KeyID is empty, payloads pass through unencrypted.
func (e *Codec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if e.KeyID == "" {
		return payloads, nil
	}
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
// MetadataEncryptionKeyID and looks up the matching key. A single Codec{}
// instance therefore decodes payloads produced under any endpoint's keyID.
func (e *Codec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
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
		if err := result[i].Unmarshal(b); err != nil {
			return payloads, err
		}
	}

	return result, nil
}
