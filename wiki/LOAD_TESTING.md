# Load Testing (k6)

The k6 scripts live in [`loadtests/`](../loadtests/) and run as TypeScript
directly (k6 v1.3.0+ transpiles them; `npm run type-check` is the optional
`tsc --noEmit` pass). Every script reads `K6_BASE_URL` (default
`http://localhost:8080`), so point it elsewhere without editing code:

```bash
K6_BASE_URL=http://localhost:8080 make loadtest-smoke
```

The `make loadtest-*` targets are listed by `make help`. This page covers the
scenarios whose results need more than their script's header to read, one
section each. The products scenarios behind `loadtest-smoke`, `loadtest-crud`,
`loadtest-read`, `loadtest-ramp`, `loadtest-spike` and `loadtest-sustained` are
described in the header comment of their script under `loadtests/`, and the
knobs that tune them (database pool size, rate limit, slow-query threshold) are
listed under "Performance tuning" in CLAUDE.md's Load Testing section.

## Tokens relays (JOSE end to end)

Each tokens relay takes a **plaintext** `{"pan": ...}` body, seals it with the
framework's outbound `JOSETransport`, calls an in-process peer simulator over
loopback HTTP (the VTS Issuer relay excepted, see below), opens the sealed
reply, and answers the standard
`{"data": {"token": ...}}` envelope. k6 performs no crypto itself, so one call
is the whole JOSE round trip: two seals and two opens across two HTTP hops.
The tokens module does no database I/O (tokenization is an HMAC), so latency is
dominated by RSA and AES-GCM CPU cost. Watch the Go runtime panels of the
Application Overview dashboard for allocator pressure.

| Script | Endpoint | Wire shape to the simulator | Targets |
| --- | --- | --- | --- |
| `tokens-relay.ts` | `POST /tokens/relay` | Nested JWE-of-JWS (sign, then encrypt), `application/jose` | `loadtest-tokens`, `loadtest-tokens-smoke` |
| `tokens-mle-relay.ts` | `POST /tokens/mle-relay` | Visa MLE: bare JWE (`A128GCM`, no signature) inside `{"encData": ...}`, `application/json` | `loadtest-tokens-mle`, `loadtest-tokens-mle-smoke` |
| `tokens-vts-issuer-relay.ts` | `POST /tokens/vts-issuer-relay` | VTS Issuer: JWS-of-JWE (encrypt `A256GCM`, then sign `PS256`), `application/jose`, peer in the client's transport | `loadtest-tokens-vts`, `loadtest-tokens-vts-smoke` |

**Fixtures.** Every tokens script draws its PAN from `TEST_PANS` in
[`loadtests/tokens-common.ts`](../loadtests/tokens-common.ts): published,
Luhn-valid network test numbers only. No real card data belongs in that list.

**What passes.** Every tokens script is built on `relayScenario`, so each
checks the status is 200 **and** the response shape for the PAN it sent: a
`tok_` token, `last4` equal to the PAN's last four digits, and a `masked_pan`
that is asterisks plus those four digits, so a full PAN coming back fails the
check. Thresholds: `p(95) < 800ms`, `p(99) < 1500ms`, HTTP failure
rate below 0.5%, token success rate above 99%.

**What gets logged.** Request bodies carry a PAN, so these scripts never print a
request or response body. The first failure prints the HTTP status and the
error envelope's `error.code` (or k6's transport error), and later failures only
increment `<prefix>_status_code{code:...}`. The per-code counter tells a 429
from the rate limiter apart from a 500 caused by a key or policy problem.

**Peer labels.** Each relay's httpclient is built with `WithPeerName`
(`tokens-peer-sim`, `visa-mle-peer-sim`), so its outbound httpclient metrics
carry a low-cardinality `peer` label for the length of a run. The same name
appears in the error if a simulator ever answers a 2xx that its client could not
unwrap: `httpclient.ErrJOSEPlaintextResponse`, which the transport refuses
rather than hand back unauthenticated.

**Controlled A/B runs.** Every tokens script, through `relayScenario`, honours
the same `PERF_RATE` / `PERF_DURATION` / `PERF_PREALLOC` / `PERF_MAXVUS` knobs as
the products tests (see `resolveScenario` in
[`loadtests/config.ts`](../loadtests/config.ts)). With `PERF_RATE` set they switch
to a constant-arrival-rate scenario and drop think time. With
`PERF_SUMMARY_FILE` set they also write the full summary JSON to that file:

```bash
PERF_RATE=50 PERF_DURATION=60s PERF_SUMMARY_FILE=perf-results/mle.json \
  k6 run loadtests/tokens-mle-relay.ts
```

**Comparing shapes.** Run the nested and MLE scripts at the same offered rate.
MLE carries no signature, so the gap between the two is roughly what two RSA
signatures and two verifications cost on your hardware.

### VTS Issuer relay (JWS-of-JWE)

`tokens-vts-issuer-relay.ts` drives `POST /tokens/vts-issuer-relay`, the Visa
Token Service Issuer shape (go-bricks v0.65.0, ADR-111): the relay encrypts
first (inner JWE, `A256GCM`, `typ: JOSE`, millisecond `iat`) and then signs the
compact JWE (outer JWS, `PS256`, `cty: JWE`). The peer verifies before it
decrypts and answers the same way. It is built on `relayScenario`, so the
fixtures, shape check, thresholds, logging rule and `PERF_*` knobs above all
apply, and its client carries the peer label `visa-vts-issuer-peer-sim`.

One difference matters when you compare numbers: this relay's peer is not a
`/__sim/` route. The Issuer body is a bare compact on `application/jose` in
both directions, and a go-bricks route that can carry the `simulator` tag
answers JSON only, so the simulator is plugged in as the relay client's base
transport (`httpclient.Builder.WithTransport`), under the framework's
`JOSETransport`. A call therefore makes one HTTP hop (k6 to the relay), not
two. It performs the same RSA operation count as `tokens-relay.ts` (two
signatures, two verifications, two RSA-OAEP wraps and unwraps), so at the same
offered rate the gap between the two is roughly the loopback hop plus the
nesting order, not extra crypto.

```bash
make loadtest-tokens-vts-smoke   # 1 VU, 30s
PERF_RATE=50 PERF_DURATION=60s PERF_SUMMARY_FILE=perf-results/vts.json \
  k6 run loadtests/tokens-vts-issuer-relay.ts
```
