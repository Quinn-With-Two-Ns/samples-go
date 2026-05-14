# Nexus Per-Endpoint Encryption

This sample demonstrates encrypting Temporal payloads with a different
encryption key per Nexus endpoint. End-to-end:

```
                       endpoint-a  --> key-a
                      /
   caller workflow --+
                      \
                       endpoint-b  --> key-b
```

The caller and handler workers are both configured with the same encrypting
`DataConverter` plus a small `WorkerInterceptor` that maps the active Nexus
endpoint to a `keyID` and seeds it into context. The `DataConverter`
implements `workflow.ContextAware`, so payloads encoded by either workflow
carry the correct `MetadataEncryptionKeyID` and stay encrypted at rest in
both the caller and handler namespaces.

This sample composes three existing patterns; it does not reinvent any:

- Crypto + context-aware codec from `../encryption/`.
- Two-interceptor (workflow outbound + Nexus inbound) shape from
  `../nexus-context-propagation/`.
- Caller / handler / starter topology from `../nexus/`.

## How it works

The `DataConverter` does not know about endpoints. It only knows how to read a
`CryptContext{KeyID: ...}` from the workflow or Go context and encrypt
payloads with that key. The interceptors are what bind endpoint to key:

- Caller side -- `WorkerInterceptor.InterceptWorkflow` returns an outbound
  interceptor whose `ExecuteNexusOperation` reads
  `input.Client.Endpoint()`, looks up the `keyID` in `EndpointKeys`, and wraps
  the workflow context with `CryptContext{KeyID: keyID}` for the duration of
  the Nexus call.
- Handler side -- `WorkerInterceptor.InterceptNexusOperation` returns an
  inbound interceptor whose `StartOperation` reads
  `temporalnexus.GetOperationInfo(ctx).Endpoint`, looks up the `keyID` in the
  same `EndpointKeys` map (configured identically on both workers), and
  attaches `CryptContext` to the Go context. The handler client's
  `ContextPropagator` then carries `CryptContext` into the handler-started
  workflow.

The starter also pre-seeds `CryptContext` on the Go context before calling
`ExecuteWorkflow`, so the caller workflow's own input and result payloads
(event #1 onward) are encrypted under the per-endpoint key. Without this step
the workflow input/result would fall back to the default codec state.

A single `codec-server` registers a `Codec{}` that picks the decryption key
per payload by reading `MetadataEncryptionKeyID`. Payloads from either
endpoint decode through the same server.

## Prerequisites

- A running [Temporal service](https://github.com/temporalio/samples-go/tree/main/#how-to-use).
  The dev server (>= 1.30.0) works:

  ```
  temporal server start-dev
  ```

  The 1.30.0 floor is required so that `temporalnexus.GetOperationInfo(ctx).Endpoint`
  is populated on the handler side. See the appendix for the fallback on older
  servers.
- Two Nexus endpoints, both targeting the handler task queue:

  ```
  temporal operator nexus endpoint create \
    --name endpoint-a \
    --target-namespace default \
    --target-task-queue nexus-per-endpoint-encryption-handler-tq

  temporal operator nexus endpoint create \
    --name endpoint-b \
    --target-namespace default \
    --target-task-queue nexus-per-endpoint-encryption-handler-tq
  ```

## Steps to run

1. Start the handler worker:
   ```
   go run ./nexus-per-endpoint-encryption/handler/worker
   ```
2. Start the caller worker:
   ```
   go run ./nexus-per-endpoint-encryption/caller/worker
   ```
3. Start the codec server (used to decode payloads with the Temporal CLI):
   ```
   go run ./nexus-per-endpoint-encryption/codec-server
   ```
4. Run the starter. By default it triggers one workflow per endpoint:
   ```
   go run ./nexus-per-endpoint-encryption/caller/starter
   ```
   Pass `-endpoint endpoint-a` or `-endpoint endpoint-b` to run a single one.

## What to observe

- In the Temporal UI, list the workflows produced by the starter. Each will
  have a corresponding handler workflow in the same namespace.
- Inspect any workflow's events. The raw payloads carry
  `encryption-key-id = key-a` for the `endpoint-a` runs and
  `encryption-key-id = key-b` for the `endpoint-b` runs.
- Run `temporal workflow show --workflow-id <id>` to confirm payloads are not
  human-readable without the codec.
- Re-run with `--codec-endpoint http://localhost:8081/` to decode them:
  ```
  temporal workflow show --workflow-id <id> --codec-endpoint http://localhost:8081/
  ```
  The same codec server decodes both endpoints' payloads.

## Architecture summary

- One context-aware `DataConverter` (with `Compress: true`).
- One `ContextPropagator` carrying `CryptContext` across workflow and Go
  context boundaries.
- One `WorkerInterceptor` registered on both worker processes.
- One `EndpointKeys map[string]string` literal wired into both worker mains.
- One codec server.

## Production caveats

- **Shared map is sample-only.** This sample wires the same Go literal into
  both worker mains so that the keyID derived on the caller side and on the
  handler side agrees. A real deployment should resolve per-endpoint keys
  from a KMS independently on each side; only the keyID metadata flows over
  the wire and on the payload, never the key material.
- **Mock `getKey`.** `crypt.go` defines two hardcoded 32-byte AES keys keyed
  by keyID. Replace with a KMS lookup before shipping.
- **Codec-server has both keys and is unauthenticated.** The same caveat
  applies: the codec server in this sample knows both `key-a` and `key-b`
  because the demo wires the literal in. It also binds to `0.0.0.0` with no
  authentication, so anything that can reach the port can decode payloads. A
  production codec server must source key material from a KMS, authenticate
  the caller (e.g. mTLS or OAuth), and restrict network exposure.
- **Per-endpoint, not per-operation.** The sample keys at endpoint
  granularity. Per-operation or per-workflow keying would require additional
  interceptor logic.
- **Headers are not encrypted.** Only payloads are. Nexus headers travel in
  plain text.

## Appendix -- Fallback for Temporal server < 1.30.0

On servers older than 1.30.0, `temporalnexus.GetOperationInfo(ctx).Endpoint`
returns an empty string. The handler-side interceptor in this sample relies on
that field. On older servers the standard workaround is the
"caller writes header / handler reads header" bridge demonstrated by the
[`nexus-context-propagation`](../nexus-context-propagation/) sample. The
caller-side outbound interceptor writes the `CryptContext` (or just the
`keyID`) into `input.NexusHeader[propagationKey]`, and the handler-side
inbound interceptor reads it from `input.Options.Header[propagationKey]`.
This couples handler correctness to caller behavior, but is the only viable
path on servers that do not populate the endpoint on the handler side.
