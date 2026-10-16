# Keystore dotted entry names: operator decision record

The demo renames its five keystore entries to dotted names. go-bricks
[ADR-144](https://github.com/gaborage/go-bricks/blob/v0.72.0/wiki/adr_144_dotted_keystore_entry_names.md)
(migration atom C72.17) makes a `keystore.keys` name a path of segments,
written as nested YAML. The environment reaches the same path, so every new
name can be set from any shell. The pin in `go.mod`, go-bricks `v0.72.0`, is
the first release that carries C72.17; this change moves no dependency. The
upgrade that brought the grammar in is
[GOBRICKS_V072_UPGRADE.md](GOBRICKS_V072_UPGRADE.md).

## At a glance

- **Five entries renamed.** The DER files in `certs/` keep their names, and the
  secrets keep their contents. Only the entry names, and so the variable names,
  change.
- **The names are on the wire.** The webhook `keyName`, the JOSE kids and the
  sealed kids, `iss` and inbox keys all move. Two of them are external
  contracts.
- **The sealing families are renamed, not rotated.** A body sealed under the
  old family cannot be opened after the cutover, so the payments queues are
  drained first.
- **Deploy order:** add the new variables, drain, deploy, then remove the old
  variables. The keystore accepts entries nothing reads, so there is no gap.

## What changes

### Rename map

| Old name | New name | Kind | Used by |
|----------|----------|------|---------|
| `webhooksigning` | `webhooks.signing` | RSA pair | webhooks module, on each request |
| `tokensour` | `tokens.our` | RSA pair, JOSE kid | tokens module: `jose:` tags, relays |
| `tokenspeer` | `tokens.peer` | RSA pair, JOSE kid | tokens module: `jose:` tags, simulators |
| `paymentssign-v1` (family `paymentssign`) | `payments.sign.v1` (family `payments.sign`) | sealing sign generation | payments module, `seal:` tag |
| `paymentsencrypt-v1` (family `paymentsencrypt`) | `payments.encrypt.v1` (family `payments.encrypt`) | sealing encrypt generation | payments module, `seal:` tag |

A dotted family names its generations with a final `v<N>` segment, so the next
sign generation is `payments.sign.v2`, never `payments.sign-v2`.

### Environment variables

Each entry takes `_PUBLIC_` and `_PRIVATE_`, each with `_VALUE` (base64 DER)
or `_FILE` (a path). One example per entry:

| Old variable | New variable | POSIX `export` |
|--------------|--------------|----------------|
| `KEYSTORE_KEYS_WEBHOOKSIGNING_PRIVATE_VALUE` | `KEYSTORE_KEYS_WEBHOOKS_SIGNING_PRIVATE_VALUE` | yes, both |
| `KEYSTORE_KEYS_TOKENSOUR_PRIVATE_VALUE` | `KEYSTORE_KEYS_TOKENS_OUR_PRIVATE_VALUE` | yes, both |
| `KEYSTORE_KEYS_TOKENSPEER_PRIVATE_VALUE` | `KEYSTORE_KEYS_TOKENS_PEER_PRIVATE_VALUE` | yes, both |
| `KEYSTORE_KEYS_PAYMENTSSIGN-V1_PRIVATE_VALUE` | `KEYSTORE_KEYS_PAYMENTS_SIGN_V1_PRIVATE_VALUE` | old: no; new: yes |
| `KEYSTORE_KEYS_PAYMENTSENCRYPT-V1_PRIVATE_VALUE` | `KEYSTORE_KEYS_PAYMENTS_ENCRYPT_V1_PRIVATE_VALUE` | old: no; new: yes |

The rotation selector moves too. No environment sets it today.

| Old | New |
|-----|-----|
| `messaging.seal.active.paymentssign: v2` | `messaging.seal.active: {payments: {sign: v2}}`, or `MESSAGING_SEAL_ACTIVE_PAYMENTS_SIGN=v2` |

### Wire strings

| Where | Old | New |
|-------|-----|-----|
| Signed webhook payload, `keyName` | `webhooksigning` | `webhooks.signing` |
| `POST /tokens` request: JWE `kid` / inner JWS `kid` | `tokensour` / `tokenspeer` | `tokens.our` / `tokens.peer` |
| `POST /tokens` reply: JWE `kid` / inner JWS `kid` | `tokenspeer` / `tokensour` | `tokens.peer` / `tokens.our` |
| Relay requests: encrypt `kid` / sign `kid` (the MLE relay only encrypts) | `tokenspeer` / `tokensour` | `tokens.peer` / `tokens.our` |
| Sealed event, outer JWS `kid` | `paymentssign-v1` | `payments.sign.v1` |
| Sealed event, inner JWE `kid` | `paymentsencrypt-v1` | `payments.encrypt.v1` |
| Sealed event, inner JWE `iss` | `paymentssign-v1` | `payments.sign.v1` |
| Inbox ledger key (`gobricks_inbox.event_id`) | `paymentssign:<jti>` | `payments.sign:<jti>` |

### External contracts

- **Webhook verifiers.** Every signed payload carries `keyName`. A verifier
  outside this service that pins `webhooksigning` must accept
  `webhooks.signing` before the cutover.
- **JOSE peers.** The kids are in every JOSE header. In this demo both peers
  are in-process simulators, so nothing outside the service changes. A real
  partner pins the kids: confirm it accepts `.` in a kid and give it the new
  kids before the cutover (framework `wiki/keystore.md`, runbook R2).
- **Sealed-event consumers.** The family is in the sealed `kid`, the inner
  `iss` and the inbox key. Here the producer and the only consumer are one
  process. A consumer in another service would change its `seal:` tag and its
  keystore entries in the same cutover.

## Per-environment operator changes required before deploy

### Development (`config.development.yaml`, docker compose)

- The keystore block is nested: `webhooks: {signing: …}`, `tokens: {our: …,
  peer: …}` and `payments: {sign: {v1: …}, encrypt: {v1: …}}`. `tokens:` and
  `payments:` are namespaces: a node is an entry only when it holds `public`,
  `private`, `secret` or `pkcs12`.
- `make generate-keys` is unchanged and writes the same DER files.
- **Retained broker volume.** A body sealed under the old family that is still
  on `payments.authorized.dlq` (the seal-event demo parks one per run) stays
  there and is harmless: the demo matches its own parked body by bytes. Copies
  on the consumerless `payments.authorized.tap` are harmless too:
  `make show-sealed-message` purges the tap before it publishes, and the 1h
  TTL empties it anyway.
- **Retained postgres volume.** Old `paymentssign:<jti>` rows stay in
  `gobricks_inbox` until the retention sweep removes them. See the runbook,
  step 10.

### Non-development deploy (env vars, per [DEPLOYING.md](DEPLOYING.md))

The secret contents do not change: each new variable gets the same base64 DER
(or the same mounted file) as the variable it replaces.

**The keystore tolerates entries nothing reads.** A local boot with the five
old variables set beside the five dotted entries started cleanly: 10 entries
loaded, `/health` and `/ready` 200, no keystore warning. Read with `-` as `.`,
no old name equals or nests a new one (`tokensour` against `tokens.our`,
family `paymentssign` against `payments.sign`), so ADR-144's look-alike and
nesting rules do not refuse the pair. So the order is:

1. Add the new variables beside the old ones.
2. Drain the sealing families (runbook below).
3. Deploy the renamed binary.
4. Remove the old variables.

There is no window in which a running binary lacks its keys.

**Precondition.** The binary running when step 1 lands must already be built
from a go-bricks version that carries C72.17. The first release that does is
v0.72.0. In this repo, every commit from `c5bfc0c` (the `8cfb522f` main pin)
onwards carries it, per [GOBRICKS_V072_UPGRADE.md](GOBRICKS_V072_UPGRADE.md). ADR-144
records that an earlier release reads a variable such as
`KEYSTORE_KEYS_TOKENS_OUR_PUBLIC_VALUE` as a phantom entry `tokens` and fails
startup. That was not re-run here.

**Keeping the old sealing keys does not keep old bodies openable.** The old
and new families are two families. With both sets provisioned, the renamed
binary still refused a body sealed under `paymentssign-v1` with
`SEAL_KID_FAMILY_MISMATCH` and parked it on the DLQ. The drain is therefore
needed whatever stays provisioned.

#### Drain-then-cutover runbook (sealing families)

1. **List every producer and consumer of `payment.authorized`.** In this demo
   both are the one service. Any other service that seals or opens this event
   joins the same cutover with its own tag and keystore change.
2. **Add the new variables** (step 1 above). This is not a key ceremony: the
   material is the same.
3. **Stop new payments under the old family.** Take `POST /payments/authorize`
   out of traffic, and keep it out until step 6. It is the only producer of
   sealed payments, so what is left in flight is what is already on the
   broker.
4. **Pass the drain gate:**
   - `payments.authorized`: zero ready and zero unacknowledged messages.
   - `payments.authorized.dlq`: empty, or every message triaged. Not every
     parked body is a payment:
     - **Worth replaying, before the cutover:** a valid `payment.authorized`
       event sealed under the old family that was parked because its handler
       failed. Only the old binary can open it. Its clear `orderId` identifies
       it. In this demo the handler has no payment side effect: it writes the
       inbox dedup row and logs (`onPaymentAuthorized` in
       `internal/modules/payments/module.go`), so a replay only completes that
       record.
     - **Discard:** test bodies, such as the seal-event demo's wrong-event-type
       body (`SEAL_EVENT_TYPE_MISMATCH`), which piles up across runs, and any
       body the opener refused with a non-recoverable `SEAL_*` code, which
       fails the same way on replay.
   - `payments.authorized.tap`: consumerless and demo-only, never on the
     payment path. Purge it, or let its 1h TTL empty it.
   - The demo seals nothing through the outbox, enables no inbox hold, and
     keeps no sealed-bytes store, so the framework's wider drain gate
     (`wiki/sealing.md`, "Rotation runbooks") has nothing more here. If any of
     those is added, it joins the gate.
5. **Deploy the renamed binary.** Tags, kids and keystore names move together
   in one release. With more than one replica, a rolling deploy runs old and
   new binaries side by side on the same `payments.authorized` queue, and each
   parks the other's bodies with `SEAL_KID_FAMILY_MISMATCH`: the old binary
   declares the family `paymentssign`, the new one `payments.sign`, and the
   opener checks the wire kid's family against the declared one before it
   looks up any key (rule 3; upstream `jose/sealed/open.go`, `peekOuter`).
   Keeping authorize out of traffic is what keeps that queue empty during the
   rollout.
6. **Reopen authorize once no replica runs the old binary.** Wait until the
   rollout reports every replica on the renamed release, then put
   `POST /payments/authorize` back in traffic, before the test payment in
   step 7.
7. **Verify:**
   - The startup log line `KeyStore initialized successfully` counts 10 while
     both sets are present, and `/ready` answers 200.
   - One payment: the sealed `kid` is `payments.sign.v1` (locally,
     `make show-sealed-message` prints it) and the new inbox row is
     `payments.sign:<jti>`.
   - One webhook sign and verify: the webhooks module reads its key on each
     request, so a clean boot proves nothing about `webhooks.signing`.
   - One call to each tokens relay.
8. **Watch the DLQ.** A body sealed under the old family that arrives after
   the cutover is refused with `SEAL_KID_FAMILY_MISMATCH`. That code is not
   recoverable, and the message is parked, never processed twice. Reconcile it
   on its `orderId`; it can never be opened by the renamed binary, so do not
   replay it.
9. **Remove the old variables**, all ten (both halves of five entries).
10. **Leave the old inbox rows.** `paymentssign:<jti>` can never equal a key
    under the new family, so a stale row deduplicates nothing. The inbox
    retention sweep deletes processed rows by age, whatever their key
    (`inbox.retentionperiod`, 168h; upstream `inbox/cleanup.go` and
    `inbox/store_postgres.go` `DeleteProcessed`, and `wiki/sealing.md`,
    "Renaming a family").

**Rollback** is a reverse drain. An earlier binary declares the old family, so
it refuses bodies sealed under `payments.sign` with `SEAL_KID_FAMILY_MISMATCH`
(checked with `open-event`). An earlier binary may also fail startup on the
new variables (see the precondition). So:

1. Take `POST /payments/authorize` out of traffic.
2. Pass the drain gate of step 4 for `payments.sign` bodies.
3. Roll back the code and the variables together. A rolling rollback with more
   than one replica cross-parks bodies exactly as step 5 describes, which is
   why authorize stays out of traffic throughout.
4. Reopen authorize once no replica runs the renamed binary, then send one
   test payment.

#### Failure modes measured locally

| Shape | Result |
|-------|--------|
| Old and new names both set | Boots: 10 entries, `/health` and `/ready` 200, no keystore warning. |
| Renamed binary, old variables only (non-development boot) | Startup refused: `logical kid "payments.sign" has no provisioned generation (expected a keystore.keys entry named payments.sign.v<N>)`. |
| Every name renamed except the webhook pair | Boots, `/ready` 200. `POST /webhooks/sign` answers 500, and the log reads `keystore: key "webhooks.signing" not found`. |
| A leftover old public half alone (`KEYSTORE_KEYS_PAYMENTSSIGN-V1_PUBLIC_FILE`) | Boots in silence with a stray public-only entry (6 entries). Remove it in step 9. |
| A leftover old private half alone (`KEYSTORE_KEYS_PAYMENTSSIGN-V1_PRIVATE_FILE`) | Startup refused: `keystore.keys.paymentssign-v1.public key source required`. The error suggests completing the entry, which is the wrong fix: remove the variable. |
| A POSIX respelling of an old name (`KEYSTORE_KEYS_PAYMENTSSIGN_V1_PUBLIC_FILE`) | Startup refused: `family "paymentssign" has no '.', so its generations are named paymentssign-v<N>`. Use the new name instead. |
| A body sealed under the old family, after the cutover | Refused with `SEAL_KID_FAMILY_MISMATCH` and parked on the DLQ, with the old keys still provisioned or not. |

### Multi-tenant migrate CLI (`config.multitenant.yaml`, `go-bricks-migrate`)

None. `config.multitenant.yaml` has no `keystore` section, and the migrate CLI
reads no key material.

## Rejected alternatives

- **Keep the interim dashless names** (`webhooksigning`, `tokensour`,
  `paymentssign-v1`). They stay valid one-segment names, and ADR-144 advises
  leaving live families alone. Rejected for this repo because it is the
  showcase: it should show the form ADR-144 recommends for new keys. Also, the
  sealing variables need a `-`, which a POSIX `export` cannot set. The cost is
  the drain above, accepted because no partner pins these kids.
- **Rename without a drain.** Every body still on the broker under the old
  family would park as `SEAL_KID_FAMILY_MISMATCH`: a payment answered 202 that
  is never processed. Rejected.

## Verification

- `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` and
  `make lint` ran with `GOWORK=off`, so they compiled against the `go.mod` pin.
  `TestConfigLayoutDevelopmentLoadsBaseAndOverlay` loads and validates the
  nested development keystore block.
- Live, on the local docker stack: `make show-sealed-message` (wire `kid`
  `payments.sign.v1`, inner `kid` `payments.encrypt.v1`, `iss`
  `payments.sign.v1`, PAN absent from the wire) and `make seal-event-demo`
  (open, dedup replay on `payments.sign:<jti>`, `SEAL_EVENT_TYPE_MISMATCH`)
  passed. `make seal-payload` into `POST /tokens` and the three relays answered
  200, with JOSE `kid` `tokens.our` and `tokens.peer`. A webhook round trip
  returned `keyName` `webhooks.signing`. Every row of the failure-mode table
  was booted or sent as described.
- Not verified here: an earlier binary booted with the new variables, and a
  real partner's acceptance of `.` in a kid.
