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
`PayloadCodec` wrapped via `converter.NewCodecDataConverter`. The codec
implements `converter.PayloadCodecWithSerializationContext` so the SDK hands
it a `NexusSerializationContext` at every Nexus serialization boundary; the
codec turns the endpoint name into a keyID via `EndpointKeys` and encrypts
under that key.

## How it works

`converter.NexusSerializationContext` is supplied by the SDK at every Nexus
code boundary:

- the caller's `ExecuteNexusOperation` input/result encoding,
- the handler's Nexus task input/sync-result encoding, and
- the spawned workflow's input/output for workflow-run operations.

The codec's `WithSerializationContext` method receives that context, reads
`Endpoint`, looks up the keyID, and returns a `Codec{KeyID: ...}`. At
non-Nexus boundaries (plain workflow / activity contexts) it stays in
pass-through mode and payloads are not encrypted. Decode reads the
`MetadataEncryptionKeyID` stamped on each payload, so a single `Codec{}`
decodes payloads from either endpoint -- which is what the codec server
relies on.

No interceptors, no context propagator, no manual header stamping.

## Prerequisites

- A running [Temporal service](https://github.com/temporalio/samples-go/tree/main/#how-to-use)
  built with Nexus serialization-context support. The dev server works:

  ```
  temporal server start-dev
  ```

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
4. Run the starter. It triggers a single caller workflow that calls four
   Nexus operations -- echo + hello on each of `endpoint-a` and `endpoint-b`:
   ```
   go run ./nexus-per-endpoint-encryption/caller/starter
   ```

## What to observe

- In the Temporal UI, find the caller workflow plus the two `callee_*`
  handler workflows (one per workflow-run Nexus operation).
- Inspect the caller workflow's events. The four Nexus operation
  scheduled/completed events carry payloads tagged with
  `encryption-key-id = key-a` (for `endpoint-a` calls) and `key-b` (for
  `endpoint-b` calls) -- both keys appear in the same history. The caller
  workflow's own input/result are not encrypted: they serialize under a
  `WorkflowSerializationContext`, which the codec leaves in pass-through.
- Run `temporal workflow show --workflow-id <id>` to confirm the Nexus
  payloads are not human-readable without the codec.
- Re-run with `--codec-endpoint http://localhost:8081/` to decode them:
  ```
  temporal workflow show --workflow-id <id> --codec-endpoint http://localhost:8081/
  ```
  The same codec server decodes both endpoints' payloads.

## Architecture summary

- One `PayloadCodec` implementing `PayloadCodecWithSerializationContext`.
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
- **Codec-server has both keys and is unauthenticated.** The codec server in
  this sample knows both `key-a` and `key-b` because the demo wires the
  literal in. It also binds to `0.0.0.0` with no authentication, so anything
  that can reach the port can decode payloads. A production codec server
  must source key material from a KMS, authenticate the caller (e.g. mTLS or
  OAuth), and restrict network exposure.
- **Per-endpoint, not per-operation.** The sample keys at endpoint
  granularity. Per-operation keying would walk the
  `NexusSerializationContext.Operation` field instead.
- **Security note from the SDK docs.** `Endpoint`, `Service`, and
  `Operation` on `NexusSerializationContext` originate from server-delivered
  task/header data. Treat them as untrusted input if used in
  security-sensitive operations.
- **Headers are not encrypted.** Only payloads are. Nexus headers travel in
  plain text.
