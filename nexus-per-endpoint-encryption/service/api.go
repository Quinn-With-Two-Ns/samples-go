// Package service defines the Nexus service contract shared by the caller and
// handler in the nexus-per-endpoint-encryption sample.
package service

const HelloServiceName = "endpoint-encryption-service"

// HelloOperationName is the single operation exposed by the service. The
// sample uses two distinct Nexus endpoints that both route to this operation
// so that the per-endpoint encryption key selection is observable end-to-end.
const HelloOperationName = "say-hello"

type HelloInput struct {
	Name string
}

type HelloOutput struct {
	Message string
}
