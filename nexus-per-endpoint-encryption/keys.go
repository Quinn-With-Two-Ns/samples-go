package nexusperendpointencryption

// EndpointKeys is the canonical endpoint -> keyID map used by both the caller
// and handler workers in this sample. In production each worker process would
// resolve its own per-endpoint key material independently (e.g. from KMS);
// here we share a single Go literal so the sample is self-contained.
var EndpointKeys = map[string]string{
	"endpoint-a": "key-a",
	"endpoint-b": "key-b",
}
