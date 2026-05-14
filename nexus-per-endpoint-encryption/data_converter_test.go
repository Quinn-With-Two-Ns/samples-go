package nexusperendpointencryption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
)

// Test_DataConverter_EndpointA verifies that encoding under CryptContext for
// key-a tags payloads with MetadataEncryptionKeyID = "key-a" and that
// round-trip decode succeeds via the same data converter.
func Test_DataConverter_EndpointA(t *testing.T) {
	ctx := context.WithValue(context.Background(), PropagateKey, CryptContext{KeyID: "key-a"})
	dc := NewEncryptingDataConverter(converter.GetDefaultDataConverter(), DataConverterOptions{})

	encrypted, err := dc.WithContext(ctx).ToPayloads("Testing endpoint-a")
	require.NoError(t, err)

	require.Equal(t, []byte("key-a"), encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID])
	require.Equal(t, []byte(MetadataEncodingEncrypted), encrypted.Payloads[0].Metadata[converter.MetadataEncoding])

	var result string
	require.NoError(t, dc.FromPayloads(encrypted, &result))
	require.Equal(t, "Testing endpoint-a", result)
}

// Test_DataConverter_EndpointB verifies the per-key tagging differs from the
// endpoint-a case.
func Test_DataConverter_EndpointB(t *testing.T) {
	ctx := context.WithValue(context.Background(), PropagateKey, CryptContext{KeyID: "key-b"})
	dc := NewEncryptingDataConverter(converter.GetDefaultDataConverter(), DataConverterOptions{})

	encrypted, err := dc.WithContext(ctx).ToPayloads("Testing endpoint-b")
	require.NoError(t, err)

	require.Equal(t, []byte("key-b"), encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID])

	var result string
	require.NoError(t, dc.FromPayloads(encrypted, &result))
	require.Equal(t, "Testing endpoint-b", result)
}

// Test_DataConverter_CrossKey_Fails encodes under key-a, rewrites the
// payload's MetadataEncryptionKeyID to "key-b", and asserts that decode fails
// because AES-GCM authentication rejects the wrong key.
func Test_DataConverter_CrossKey_Fails(t *testing.T) {
	ctx := context.WithValue(context.Background(), PropagateKey, CryptContext{KeyID: "key-a"})
	dc := NewEncryptingDataConverter(converter.GetDefaultDataConverter(), DataConverterOptions{})

	encrypted, err := dc.WithContext(ctx).ToPayloads("cross-key test")
	require.NoError(t, err)
	require.Equal(t, []byte("key-a"), encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID])

	// Tamper with the metadata so the codec selects key-b to decrypt
	// ciphertext that was sealed with key-a.
	encrypted.Payloads[0].Metadata[MetadataEncryptionKeyID] = []byte("key-b")

	var result string
	err = dc.FromPayloads(encrypted, &result)
	require.Error(t, err, "decoding ciphertext sealed with key-a under key-b must fail")
}
