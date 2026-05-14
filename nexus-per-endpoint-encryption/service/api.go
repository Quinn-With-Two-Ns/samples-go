// Package service defines the Nexus service contract shared by the caller and
// handler in the nexus-per-endpoint-encryption sample.
package service

const HelloServiceName = "endpoint-encryption-service"

// HelloOperationName is a workflow-run operation. The handler starts a
// workflow whose at-rest payloads are encrypted under the per-endpoint key.
const HelloOperationName = "say-hello"

// EchoOperationName is a sync operation. The handler returns immediately
// without starting a workflow, so the demo also exercises the Nexus
// request/response boundary in isolation.
const EchoOperationName = "echo"

type HelloInput struct {
	Name string
}

type HelloOutput struct {
	Message string
}

type EchoInput struct {
	Message string
}

type EchoOutput struct {
	Message string
}
