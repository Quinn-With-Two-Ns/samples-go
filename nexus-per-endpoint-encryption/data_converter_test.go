package nexusperendpointencryption

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
)

// nexusCtx builds a NexusSerializationContext for the given endpoint. Service
// and Operation aren't read by the codec; they're set for realism only.
func nexusCtx(endpoint string) converter.NexusSerializationContext {
	return converter.NexusSerializationContext{
		Namespace: "default",
		Endpoint:  endpoint,
		Service:   "test-service",
		Operation: "test-op",
	}
}

// Test_Codec_PerEndpointKey verifies that WithSerializationContext picks the
// per-endpoint key and tags Encode output with MetadataEncryptionKeyID.
func Test_Codec_PerEndpointKey(t *testing.T) {
	for endpoint, keyID := range EndpointKeys {
		dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &Codec{})
		scoped := converter.WithDataConverterSerializationContext(dc, nexusCtx(endpoint))

		payloads, err := scoped.ToPayloads("Testing " + endpoint)
		require.NoError(t, err)
		require.Equal(t, []byte(keyID), payloads.Payloads[0].Metadata[MetadataEncryptionKeyID])
		require.Equal(t, []byte(MetadataEncodingEncrypted), payloads.Payloads[0].Metadata[converter.MetadataEncoding])

		var result string
		require.NoError(t, scoped.FromPayloads(payloads, &result))
		require.Equal(t, "Testing "+endpoint, result)
	}
}

// Test_Codec_NoContext_PassThrough verifies that without a NexusSerializationContext
// the codec leaves payloads unencrypted (no MetadataEncryptionKeyID, default
// encoding).
func Test_Codec_NoContext_PassThrough(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &Codec{})
	payloads, err := dc.ToPayloads("plaintext")
	require.NoError(t, err)
	require.NotEqual(t, []byte(MetadataEncodingEncrypted), payloads.Payloads[0].Metadata[converter.MetadataEncoding])
	require.Empty(t, payloads.Payloads[0].Metadata[MetadataEncryptionKeyID])
}

// Test_Codec_WorkflowContext_PassThrough verifies that a WorkflowSerializationContext
// (non-Nexus) leaves the codec in pass-through mode.
func Test_Codec_WorkflowContext_PassThrough(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &Codec{})
	scoped := converter.WithDataConverterSerializationContext(dc,
		converter.WorkflowSerializationContext{Namespace: "default", WorkflowID: "wf"})
	payloads, err := scoped.ToPayloads("plaintext")
	require.NoError(t, err)
	require.NotEqual(t, []byte(MetadataEncodingEncrypted), payloads.Payloads[0].Metadata[converter.MetadataEncoding])
}

// Test_Codec_CrossKey_Fails encodes under endpoint-a, rewrites the payload's
// MetadataEncryptionKeyID to "key-b", and asserts decode fails because
// AES-GCM authentication rejects the wrong key.
func Test_Codec_CrossKey_Fails(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &Codec{})
	scoped := converter.WithDataConverterSerializationContext(dc, nexusCtx("endpoint-a"))

	encrypted, err := scoped.ToPayloads("cross-key test")
	require.NoError(t, err)
	require.Equal(t, []byte("key-a"), encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID])

	encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID] = []byte("key-b")

	var result string
	require.Error(t, dc.FromPayloads(encrypted, &result), "decoding ciphertext sealed with key-a under key-b must fail")
}
