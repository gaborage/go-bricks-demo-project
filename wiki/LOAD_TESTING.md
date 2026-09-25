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

## Monitored runs (resource leaks and recovery)

k6 reports what the client saw. Three targets watch the server side of the
same run: goroutines, heap, RSS and database connections, judged against
[`loadtests/thresholds.yaml`](../loadtests/thresholds.yaml).

| Target | Script | What it does |
| --- | --- | --- |
| `loadtest-monitor` | [`scripts/monitor-loadtest.sh`](../scripts/monitor-loadtest.sh) | Samples the app every 10s into `loadtest-results/metrics-<timestamp>.csv` until Ctrl+C |
| `loadtest-analyze FILE=...` | [`scripts/analyze-loadtest-results.sh`](../scripts/analyze-loadtest-results.sh) | Per-phase peaks and means, plus the five `pass_fail.required_checks`; exit 1 on a critical issue |
| `loadtest-all-monitored` | [`scripts/run-loadtest-all-monitored.sh`](../scripts/run-loadtest-all-monitored.sh) | The `loadtest-all` scenarios in order, a cooldown between them, the monitor throughout, then the analysis |

**Sources.** Goroutines and heap come from the framework's debug endpoints,
which are off by default, loopback-only and served at the URL root. Start the
app with them on:

```bash
DEBUG_ENABLED=true DEBUG_ALLOWEDIPS=127.0.0.1,::1 \
DEBUG_ENDPOINTS_INFO=true DEBUG_ENDPOINTS_GC=true make run
```

RSS comes from `ps` on the process listening on `APP_URL`'s port, so it needs
a host process, not a container. Connection counts come from
`pg_stat_activity`, read with `docker exec` into `PG_CONTAINER` (default
`go-bricks-postgres`), so the host needs no `psql`. A source that does not
answer leaves its columns empty, the monitor warns once at start, and every
check that needed it reports `SKIP`. If every required check skips, the
analysis exits 2 (inconclusive) rather than passing. The advisory RSS and
connection peaks never decide the verdict: they cannot make one alone, and a
peak at its global critical threshold is reported without failing the run,
since the spike and ramp-up phases expect a nearly full connection pool.

**Phases.** The monitored run writes the running scenario into each sample
(`read_only`, `crud_mix`, `spike`, `ramp_up`, `sustained`, and `cooldown`
between them). The leak check compares the first and last quarter of the
`sustained` samples. The recovery check compares the end of `spike` with its
baseline stage. A hand-started monitor labels every row `manual`, so only the
peak checks apply to it.

**Short runs.** `K6_FLAGS` is passed to every `k6 run`, and `TESTS`,
`COOLDOWN` and `MONITOR_INTERVAL` trim the rest. This exercises the whole
pipeline in about three minutes:

```bash
K6_FLAGS="--vus 3 --duration 20s" COOLDOWN=5 MONITOR_INTERVAL=2 make loadtest-all-monitored
```

Each run gets its own directory, `loadtest-results/run-<timestamp>/`, holding
`metrics.csv`, one k6 log per scenario, the k6 summary JSON for the scripts that
honour `PERF_SUMMARY_FILE`, and `analysis.txt`.

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

## Topology repair (exchange loss under load)

`loadtests/topology-repair.ts` (`make loadtest-topology-repair`, about 2.5
minutes) deletes both AMQP exchanges while traffic is flowing and measures
what go-bricks v0.67.0 does about it (#1776/#1779, ADR-113 amendment). Every
pooled publisher now drives the topology redeclare pass. The first publish into
a deleted exchange takes the broker's 404, the client opens a replacement
channel, and that channel wakes the registry, which re-declares every exchange,
queue and binding over its own connection. The publish retries on the new
channel. Before v0.67.0 nothing triggered that pass, and every later publish
failed with `ErrPublishRetriesExhausted` until the app restarted.

The test is **destructive**: it deletes `product-events` and `payment-events`
on the broker it points at, so it is not part of `make loadtest-all`. For the
one-shot version with commentary, run `make redeclare-demo` first.

| Scenario | Executor | What it does |
| --- | --- | --- |
| `products` | constant arrival, `TOPO_PRODUCT_RATE`/s | `POST /products`. The outbox relay publishes each `product.created` to `product-events`. |
| `payments` | constant arrival, `TOPO_PAYMENT_RATE`/s | `POST /payments/authorize`. Each 202 is a sealed publish to `payment-events`, routed to `payments.authorized` (consumer) and `payments.authorized.tap` (no consumer). |
| `delete_exchanges` | one iteration at `TOPO_DELETE_AT` | `DELETE` both exchanges through the management API, then poll until both exist again and `payment-events` is bound to both queues. |
| `tap_drain` | 1 VU for the run plus `TOPO_DRAIN_TAIL` | Drains the tap through the management API and counts the distinct orders of this run. |

**Knobs.** `TOPO_PRODUCT_RATE` (5), `TOPO_PAYMENT_RATE` (5), `TOPO_DURATION`
(120s), `TOPO_DELETE_AT` (60s), `TOPO_DISRUPTION` (15s, the "disruption" phase
in the per-phase success rates), `TOPO_REPAIR_TIMEOUT` (30s),
`TOPO_DRAIN_TAIL` (20s), and `PERF_SUMMARY_FILE` for the full summary JSON. The
management API is `RABBIT_MGMT` (default `http://localhost:15672`) with
`RABBIT_USER` / `RABBIT_PASS` (default: the broker's default dev user) and
`RABBIT_VHOST`. As in `scripts/lib/rabbitmq-mgmt.sh`, plaintext `http://` is
refused for a non-loopback host. The make target maps `APP_URL` onto
`K6_BASE_URL`:

```bash
APP_URL=http://localhost:8080 RABBIT_MGMT=http://localhost:15672 \
  make loadtest-topology-repair
```

**What passes.** Thresholds cover HTTP error rate and latency only: under 1%
failed product requests and under 2% failed payment requests (the publish that
takes the 404 retries, so a short burst is tolerated but a lasting outage is
not), products p95 < 500ms and p99 < 1s, payments p95 < 800ms and p99 < 2s.
Whether the topology came back is a check plus the `topology_final_ok` gauge,
and the loss below is a reported number. Neither fails the run.

**Reading the summary.**

- *HTTP* shows each endpoint's success rate, plus the payments success rate for
  the baseline, disruption and recovered phases, so a burst of failures shows up
  confined to the repair.
- *Topology repair* shows how long after the `DELETE` each exchange, and then
  the `payment-events` bindings, came back. That time includes the wait for the
  next publish: nothing repairs until a publish trips the 404.
- *End-to-end delivery* compares the 202s with the distinct orders that reached
  the tap. The difference is **Lost in repair window**. It is exact when every
  request got a 202. Otherwise it is a range, because a request that failed may
  still have been published. The same numbers land under `topology_repair` in
  the `PERF_SUMMARY_FILE` JSON.

**Why a loss is not a failure.** The pass is not atomic: exchanges first, then
queues, then bindings. The typed payments publisher sets no `Mandatory` flag,
and the framework has no returned-message handler. So a publish that lands after
`payment-events` is back but before `payments.authorized` and the tap are
re-bound is acked by the broker and dropped as unroutable, while the caller
already got 202. That is the documented ack-and-drop window. The test measures it
instead of hiding it; operationally, treat a deleted exchange as an incident and
reconcile the payments authorized while it was repaired.

**Why count the tap by draining it.** The tap is capped at `x-max-length` 100
(drop-head), so its depth stops counting at 100. The management API also reports
queue depth and `message_stats` on the ~5s statistics tick (quorum queues
included), while `POST /api/queues/.../get` reads the queue itself. The tap
stands in for `payments.authorized`, whose consumer removes every delivery at
once, and the pass re-binds both queues one declare apart. As a cross-check,
teardown reads the broker's `message_stats.publish` for both queues after the
drain tail, which is longer than two statistics ticks. Those deltas count
duplicates too (a publish retried after its first confirm died with the old
channel arrives twice under one order id), so they should equal distinct plus
duplicates. The report warns if the tap ever reached its cap.

**What it does not cover.** `product-events` has no bound queue in this demo,
so outbox product events are unroutable by design. Its repair proves the relay's
publishes are confirmed again, not delivered. Any source's new channel replays
the whole topology, so whichever publish trips the 404 first repairs both
exchanges. The native streams lane (`product-activity` on port 5552) is declared
at startup only and does not self-repair: a deleted stream needs an app restart.

**What gets logged.** Payment bodies carry the published network test PANs the
tokens load tests use (`TEST_PANS`) and are never printed. App responses are
discarded (`responseType: 'none'`); a failed payment logs its status and phase
once per VU. Broker credentials travel only in the management API
`Authorization` header. Never add `--http-debug`, which would print both.
