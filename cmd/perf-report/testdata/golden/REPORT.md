# Example series: alpha vs beta vs gamma (2026-01-06 to 2026-01-09)

## Verdict

- **P1 S4 k6 http_req_duration p95 (ms)**
  - beta vs alpha +17.6%: changed (band ±7.8%); no sensitivity rerun: fewer than 3 unflagged reps
  - gamma vs beta +1.7%: no detectable difference (band ±5.0%); no sensitivity rerun: fewer than 3 unflagged reps
  - gamma vs alpha (derived) +19.6%: changed (band ±7.8%)
- **P2 S1 app CPU-ms per request**
  - beta vs alpha +11.2%: environment-limited: no detectable difference (band ±8.2%), but changed (band ±8.2%) with flagged reps excluded
  - gamma vs beta +1.3%: no detectable difference (band ±27.9%); holds with flagged reps excluded
  - gamma vs alpha (derived) +12.7%: changed (band ±8.2%)
- Rep extension check on reference alpha: S4 relative range 3.9% is within 10.0%: 3 reps suffice; S1 relative range 4.1% is within 5.0%: 3 reps suffice.
- Largest descriptive moves (no verdict): beta vs alpha: S4 k6 p50 +17.6%, S1 alloc bytes per request +7.7%, S1 allocs per request +6.7%; gamma vs beta: S4 k6 p50 +1.7%, S1 allocs per request +1.6%, S1 k6 p50 -1.3%.

## Versions and controls

|  | alpha | beta | gamma | alpha-aa |
|---|---|---|---|---|
| framework ref | v1.0.0 | v1.1.0 | main@abc1234 | v1.0.0 |
| node class | 4 vCPU, zone a | 4 vCPU, zone a | 4 vCPU, zone a | 4 vCPU, zone a |
| session UTC window | 2026-01-06T19:12Z to 2026-01-06T22:06Z | 2026-01-07T19:12Z to 2026-01-07T22:22Z | 2026-01-08T19:12Z to 2026-01-08T21:32Z | 2026-01-09T19:12Z to 2026-01-09T20:44Z |
| reps attempted / valid / void | 11 / 11 / 0 | 12 / 11 / 1 | 9 / 8 / 1 | 6 / 6 / 0 |
| voided reps | none | beta-S4-r2 | gamma-S2-r3 | none |
| idle mcores | 20 | 22 | 21 | 20 |
| harness tag | perf-harness-example | perf-harness-example | perf-harness-example | perf-harness-example |
| seed rows | 1000 | 1000 | 1000 | 1000 |
| offered rps by scenario | S1 50, S2 20, S3 20, S4 150 | S1 50, S2 20, S4 150 | S1 50, S2 20, S4 150 | S1 50, S4 150 |
| k6 VUs (preallocated / max) | 50 / 200, 50 / 400 | 50 / 200, 50 / 400 | 50 / 200 | 50 / 200 |

## Primary table (S4 P1, S1 P2)

| metric | alpha med [range] | beta med [range] | Δ% beta vs alpha | gamma med [range] | Δ% gamma vs beta | Δ% gamma vs alpha | band B | A/A drift | verdict |
|---|---|---|---|---|---|---|---|---|---|
| P1 S4 k6 http_req_duration p95 (ms) | 10.20 [10.00–10.40] n=3 | 12.00 [11.80–12.10] n=3 | +17.6% | 12.20 [11.90–12.30] n=3 | +1.7% | +19.6% | beta vs alpha: ±7.8%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±7.8% | 1.0% | beta vs alpha: changed (band ±7.8%); no sensitivity rerun: fewer than 3 unflagged reps<br>gamma vs beta: no detectable difference (band ±5.0%); no sensitivity rerun: fewer than 3 unflagged reps<br>gamma vs alpha: changed (band ±7.8%) |
| P2 S1 app CPU-ms per request | 2.450 [2.400–2.500] n=3 | 2.725 [2.420–2.800] n=4 | +11.2% | 2.760 [2.720–2.780] n=3 | +1.3% | +12.7% | beta vs alpha: ±8.2%<br>gamma vs beta: ±27.9%<br>gamma vs alpha: ±8.2% | 0.8% | beta vs alpha: environment-limited: no detectable difference (band ±8.2%), but changed (band ±8.2%) with flagged reps excluded<br>gamma vs beta: no detectable difference (band ±27.9%); holds with flagged reps excluded<br>gamma vs alpha: changed (band ±8.2%) |

## Per-scenario descriptive tables

Descriptive only: these rows never get a verdict word. Band B is shown for scale.

### S1 read mix

| metric | alpha med [range] | beta med [range] | Δ% beta vs alpha | gamma med [range] | Δ% gamma vs beta | Δ% gamma vs alpha | band B | A/A drift |
|---|---|---|---|---|---|---|---|---|
| offered rps | 50.0 [50.0–50.0] n=3 | 50.0 [50.0–50.0] n=4 | +0.0% | 50.0 [50.0–50.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| achieved rps (k6) | 50.0 [50.0–50.0] n=3 | 50.0 [50.0–50.0] n=4 | +0.0% | 50.0 [50.0–50.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| NR rps (Q2) | 50.0 [50.0–50.0] n=3 | 50.0 [50.0–50.0] n=4 | +0.0% | 50.0 [50.0–50.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| NR vs k6 rate gap % | 0.00 [0.00–0.00] n=3 | 0.00 [0.00–0.00] n=4 | +0.00 pp | 0.00 [0.00–0.00] n=3 | +0.00 pp | +0.00 pp | — | — |
| k6 p50 ms | 16.65 [16.20–17.10] n=3 | 16.88 [16.20–17.55] n=4 | +1.4% | 16.65 [16.20–17.10] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| k6 p95 ms | 37.00 [36.00–38.00] n=3 | 37.50 [36.00–39.00] n=4 | +1.4% | 37.00 [36.00–38.00] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| k6 p99 ms | 59.20 [57.60–60.80] n=3 | 60.00 [57.60–62.40] n=4 | +1.4% | 59.20 [57.60–60.80] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| k6 p50 ms {endpoint:list_products} | 16.32 [15.88–16.76] n=3 | 16.54 [15.88–17.20] n=4 | +1.4% | 16.32 [15.88–16.76] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| k6 p95 ms {endpoint:list_products} | 36.26 [35.28–37.24] n=3 | 36.75 [35.28–38.22] n=4 | +1.4% | 36.26 [35.28–37.24] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| k6 p99 ms {endpoint:list_products} | 55.50 [54.00–57.00] n=3 | 56.25 [54.00–58.50] n=4 | +1.4% | 55.50 [54.00–57.00] n=3 | -1.3% | +0.0% | beta vs alpha: ±10.8%<br>gamma vs beta: ±16.0%<br>gamma vs alpha: ±10.8% | 0.0% |
| NR p50 ms GET /api/v1/products | 7.50 [7.50–7.50] n=3 | 7.50 [7.50–7.50] n=4 | +0.0% | n/a | n/a | n/a | beta vs alpha: ±5.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR p50 ms GET /api/v1/products/:id | 1.50 [1.50–1.50] n=3 | 1.50 [1.50–1.50] n=4 | +0.0% | n/a | n/a | n/a | beta vs alpha: ±5.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR p95 ms GET /api/v1/products | 23.45 [23.40–23.50] n=3 | 23.73 [23.42–23.80] n=4 | +1.2% | n/a | n/a | n/a | beta vs alpha: ±5.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR p95 ms GET /api/v1/products/:id | 6.45 [6.40–6.50] n=3 | 6.73 [6.42–6.80] n=4 | +4.3% | n/a | n/a | n/a | beta vs alpha: ±5.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR p99 ms GET /api/v1/products | 40.00 [40.00–40.00] n=3 | 40.00 [40.00–40.00] n=4 | +0.0% | n/a | n/a | n/a | beta vs alpha: ±10.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR p99 ms GET /api/v1/products/:id | 8.00 [8.00–8.00] n=3 | 8.00 [8.00–8.00] n=4 | +0.0% | n/a | n/a | n/a | beta vs alpha: ±10.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| error % | 0.000 [0.000–0.000] n=3 | 0.000 [0.000–0.000] n=4 | +0.000 pp | 0.000 [0.000–0.000] n=3 | +0.000 pp | +0.000 pp | — | — |
| 429 responses | 0 [0–0] n=3 | 0 [0–0] n=4 | +0 | 0 [0–0] n=3 | +0 | +0 | — | — |
| dropped iterations | 0 [0–0] n=3 | 0 [0–0] n=4 | +0 | 0 [0–0] n=3 | +0 | +0 | — | — |
| CPU mcores (Q7) | 143 [140–145] n=3 | 158 [143–162] n=4 | +11.1% | 159 [157–160] n=3 | +0.5% | +11.6% | beta vs alpha: ±7.0%<br>gamma vs beta: ±24.0%<br>gamma vs alpha: ±7.0% | 0.7% |
| CPU-ms per request | 2.450 [2.400–2.500] n=3 | 2.725 [2.420–2.800] n=4 | +11.2% | 2.760 [2.720–2.780] n=3 | +1.3% | +12.7% | beta vs alpha: ±8.2%<br>gamma vs beta: ±27.9%<br>gamma vs alpha: ±8.2% | 0.8% |
| CPU throttled % | 0.20 [0.20–0.20] n=3 | 0.20 [0.20–1.50] n=4 | +0.00 pp | 0.20 [0.20–0.20] n=3 | +0.00 pp | +0.00 pp | — | — |
| working set avg MiB | 60.5 [60.5–60.5] n=3 | 60.5 [60.5–60.5] n=4 | +0.0% | 60.5 [60.5–60.5] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| working set max MiB | 64.0 [64.0–64.0] n=3 | 64.0 [64.0–64.0] n=4 | +0.0% | 64.0 [64.0–64.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| alloc bytes per request | 5200 [5200–5200] n=3 | 5600 [5600–5600] n=4 | +7.7% | 5650 [5650–5650] n=3 | +0.9% | +8.7% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.2% |
| allocs per request | 60.0 [60.0–60.0] n=3 | 64.0 [64.0–64.0] n=4 | +6.7% | 65.0 [65.0–65.0] n=3 | +1.6% | +8.3% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |

### S2 CRUD

| metric | alpha med [range] | beta med [range] | Δ% beta vs alpha | gamma med [range] | Δ% gamma vs beta | Δ% gamma vs alpha | band B | A/A drift |
|---|---|---|---|---|---|---|---|---|
| offered rps | 20.0 [20.0–20.0] n=3 | 20.0 [20.0–20.0] n=3 | +0.0% | 20.0 [20.0–20.0] n=2 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | n/a |
| achieved rps (k6) | 20.0 [20.0–20.0] n=3 | 20.0 [20.0–20.0] n=3 | +0.0% | 20.0 [20.0–20.0] n=2 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | n/a |
| k6 p50 ms | 22.50 [22.05–22.95] n=3 | 22.50 [22.05–22.95] n=3 | +0.0% | 22.28 [22.05–22.50] n=2 | -1.0% | -1.0% | beta vs alpha: ±8.0%<br>gamma vs beta: ±8.0%<br>gamma vs alpha: ±8.0% | n/a |
| k6 p95 ms | 50.00 [49.00–51.00] n=3 | 50.00 [49.00–51.00] n=3 | +0.0% | 49.50 [49.00–50.00] n=2 | -1.0% | -1.0% | beta vs alpha: ±8.0%<br>gamma vs beta: ±8.0%<br>gamma vs alpha: ±8.0% | n/a |
| k6 p99 ms | 80.00 [78.40–81.60] n=3 | 80.00 [78.40–81.60] n=3 | +0.0% | n/a | n/a | n/a | beta vs alpha: ±10.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| error % | 0.040 [0.040–0.040] n=3 | 0.040 [0.040–0.040] n=3 | +0.000 pp | 0.040 [0.040–0.040] n=2 | +0.000 pp | +0.000 pp | — | — |
| 429 responses | 0 [0–0] n=3 | 0 [0–0] n=3 | +0 | 0 [0–0] n=2 | +0 | +0 | — | — |
| dropped iterations | 0 [0–0] n=3 | 0 [0–0] n=3 | +0 | n/a | n/a | n/a | — | — |

### S3 tokens

| metric | alpha med [range] | beta med [range] | Δ% beta vs alpha | gamma med [range] | Δ% gamma vs beta | Δ% gamma vs alpha | band B | A/A drift |
|---|---|---|---|---|---|---|---|---|
| offered rps | 20.0 [20.0–20.0] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| achieved rps (k6) | 20.0 [20.0–20.0] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR rps (Q2) | 40.2 [40.2–40.2] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR vs k6 rate gap % | 0.50 [0.50–0.50] n=1 | n/a | n/a | n/a | n/a | n/a | — | — |
| k6 p50 ms | 36.00 [36.00–36.00] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| k6 p95 ms | 80.00 [80.00–80.00] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| k6 p99 ms | 128.00 [128.00–128.00] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| error % | 0.000 [0.000–0.000] n=1 | n/a | n/a | n/a | n/a | n/a | — | — |
| 429 responses | 0 [0–0] n=1 | n/a | n/a | n/a | n/a | n/a | — | — |
| dropped iterations | 0 [0–0] n=1 | n/a | n/a | n/a | n/a | n/a | — | — |
| CPU mcores (Q7) | 100 [100–100] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| CPU-ms per request | 4.000 [4.000–4.000] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| CPU throttled % | 0.20 [0.20–0.20] n=1 | n/a | n/a | n/a | n/a | n/a | — | — |
| working set avg MiB | 62.0 [62.0–62.0] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| working set max MiB | 66.0 [66.0–66.0] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| alloc bytes per request | 12000 [12000–12000] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| allocs per request | 150.0 [150.0–150.0] n=1 | n/a | n/a | n/a | n/a | n/a | beta vs alpha: n/a<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |

### S4 get-only

| metric | alpha med [range] | beta med [range] | Δ% beta vs alpha | gamma med [range] | Δ% gamma vs beta | Δ% gamma vs alpha | band B | A/A drift |
|---|---|---|---|---|---|---|---|---|
| offered rps | 150.0 [150.0–150.0] n=3 | 150.0 [150.0–150.0] n=3 | +0.0% | 150.0 [150.0–150.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| achieved rps (k6) | 150.0 [150.0–150.0] n=3 | 150.0 [150.0–150.0] n=3 | +0.0% | 150.0 [150.0–150.0] n=3 | +0.0% | +0.0% | beta vs alpha: ±5.0%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±5.0% | 0.0% |
| NR rps (Q2) | 150.4 [150.4–150.4] n=1 | 154.0 [154.0–154.0] n=1 | +2.4% | n/a | n/a | n/a | beta vs alpha: ±5.0%<br>gamma vs beta: n/a<br>gamma vs alpha: n/a | n/a |
| NR vs k6 rate gap % | 0.27 [0.27–0.27] n=1 | 2.67 [2.67–2.67] n=1 | +2.40 pp | n/a | n/a | n/a | — | — |
| k6 p50 ms | 4.59 [4.50–4.68] n=3 | 5.40 [5.31–5.45] n=3 | +17.6% | 5.49 [5.36–5.54] n=3 | +1.7% | +19.6% | beta vs alpha: ±7.8%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±7.8% | 1.0% |
| k6 p95 ms | 10.20 [10.00–10.40] n=3 | 12.00 [11.80–12.10] n=3 | +17.6% | 12.20 [11.90–12.30] n=3 | +1.7% | +19.6% | beta vs alpha: ±7.8%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±7.8% | 1.0% |
| k6 p99 ms | 16.32 [16.00–16.64] n=3 | 19.20 [18.88–19.36] n=3 | +17.6% | 19.52 [19.04–19.68] n=3 | +1.7% | +19.6% | beta vs alpha: ±10.0%<br>gamma vs beta: ±10.0%<br>gamma vs alpha: ±10.0% | 1.0% |
| k6 p50 ms {endpoint:get_product} | 4.50 [4.41–4.59] n=3 | 5.29 [5.20–5.34] n=3 | +17.7% | 5.38 [5.25–5.42] n=3 | +1.7% | +19.6% | beta vs alpha: ±7.8%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±7.8% | 1.0% |
| k6 p95 ms {endpoint:get_product} | 10.00 [9.80–10.19] n=3 | 11.76 [11.56–11.86] n=3 | +17.6% | 11.96 [11.66–12.05] n=3 | +1.7% | +19.6% | beta vs alpha: ±7.8%<br>gamma vs beta: ±5.0%<br>gamma vs alpha: ±7.8% | 1.0% |
| k6 p99 ms {endpoint:get_product} | 15.30 [15.00–15.60] n=3 | 18.00 [17.70–18.15] n=3 | +17.6% | 18.30 [17.85–18.45] n=3 | +1.7% | +19.6% | beta vs alpha: ±10.0%<br>gamma vs beta: ±10.0%<br>gamma vs alpha: ±10.0% | 1.0% |
| error % | 0.000 [0.000–0.000] n=3 | 0.000 [0.000–0.000] n=3 | +0.000 pp | 0.000 [0.000–0.000] n=3 | +0.000 pp | +0.000 pp | — | — |
| 429 responses | 0 [0–0] n=3 | 0 [0–0] n=3 | +0 | 0 [0–0] n=3 | +0 | +0 | — | — |
| dropped iterations | 0 [0–0] n=3 | 0 [0–0] n=3 | +0 | 0 [0–0] n=3 | +0 | +0 | — | — |

## Stress staircase (S5)

Indicative: one rep per label, its last attempt whether valid or void. Cells are p95 ms / 5xx % / CPU mcores / CPU throttled %, from the per-minute Q18 series over the minutes wholly inside each step. Steps start at perfMeta.startedAt (else RUN_START), with a ramp (perfMeta.stepRampSeconds, else 15 s) before every step after the first.

| step (offered rps) | alpha | beta |
|---|---|---|
| 1 (50) | 8.00 / 0.00 / 100 / 0.0 | n/a |
| 2 (100) | 9.00 / 0.00 / 195 / 0.0 | n/a |
| 3 (150) | 12.50 / 0.00 / 290 / 5.0 | n/a |
| 4 (200) | 25.00 / 0.00 / 385 / 30.0 | n/a |
| knee | step 3: 150 rps offered, 150.0 achieved, 290 mcores | n/a (no Q18 series) |

## Soak (optional)

perf-report does not compute leak slopes: they need per-minute working-set and goroutine series, which the consumed queries do not carry. Read them from the soak run's raw NR files.

## Telemetry comparability notes

- P1 comes from the k6 summary, so a change in what a version's telemetry emits cannot move it.
- NR latency percentiles come from histograms and are approximations.
- Per-request server metrics divide by Q2 `n` summed over the route facets k6 calls, after subtracting the label's idle level for the length of the NR window. `/__sim/` routes are left out of that divisor: a relay request reaches its in-process peer as a second server request, so a version that changes how the peer is reached cannot move the per-request numbers. The window is read back from the NR data (`n / rps` over every facet, else `cpu_s / cores`), and from windows.tsv minus the trim only when neither is present.
- The NR vs k6 rate gap leaves out `/__sim/` routes, which k6 never calls directly. A rep beyond ±2% is flagged `nr-gap`; flagged: beta-S4-r1 2.67%.
- Synthetic fixture: every number is made up to exercise the report.

## Validity log

Every attempted rep. Only valid reps enter the statistics; a superseded row was re-run under the same run_id.

| run_id | run window (UTC) | rc | valid | reason | flags | NR vs k6 gap % | throttled % |
|---|---|---|---|---|---|---|---|
| alpha-S4-r1 | 2026-01-06T19:14:00Z to 2026-01-06T19:24:00Z | 0 | 1 |  |  | 0.27 | n/a |
| alpha-S1-r1 | 2026-01-06T19:30:00Z to 2026-01-06T19:40:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-S2-r1 | 2026-01-06T19:46:00Z to 2026-01-06T19:56:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-S3-r1 | 2026-01-06T20:02:00Z to 2026-01-06T20:12:00Z | 0 | 1 |  |  | 0.50 | 0.20 |
| alpha-S1-r2 | 2026-01-06T20:18:00Z to 2026-01-06T20:28:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-S2-r2 | 2026-01-06T20:34:00Z to 2026-01-06T20:44:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-S4-r2 | 2026-01-06T20:50:00Z to 2026-01-06T21:00:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-S2-r3 | 2026-01-06T21:06:00Z to 2026-01-06T21:16:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-S4-r3 | 2026-01-06T21:22:00Z to 2026-01-06T21:32:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-S1-r3 | 2026-01-06T21:38:00Z to 2026-01-06T21:48:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-S5-r1 | 2026-01-06T21:54:00Z to 2026-01-06T22:06:46Z | 0 | 1 |  |  | n/a | n/a |
| beta-S4-r1 | 2026-01-07T19:14:00Z to 2026-01-07T19:24:00Z | 0 | 1 |  | nr-gap | 2.67 | n/a |
| beta-S1-r1 | 2026-01-07T19:30:00Z to 2026-01-07T19:40:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| beta-S2-r1 | 2026-01-07T19:46:00Z to 2026-01-07T19:56:00Z | 0 | 1 |  |  | n/a | n/a |
| beta-S1-r2 | 2026-01-07T20:02:00Z to 2026-01-07T20:12:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| beta-S2-r2 | 2026-01-07T20:18:00Z to 2026-01-07T20:28:00Z | 0 | 1 |  |  | n/a | n/a |
| beta-S4-r2 | 2026-01-07T20:34:00Z to 2026-01-07T20:44:00Z | 3 | 0 (superseded) | dropped |  | n/a | n/a |
| beta-S2-r3 | 2026-01-07T20:50:00Z to 2026-01-07T21:00:00Z | 0 | 1 |  |  | n/a | n/a |
| beta-S4-r3 | 2026-01-07T21:06:00Z to 2026-01-07T21:16:00Z | 0 | 1 |  |  | n/a | n/a |
| beta-S1-r3 | 2026-01-07T21:22:00Z to 2026-01-07T21:32:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| beta-S1-r4 | 2026-01-07T21:38:00Z to 2026-01-07T21:48:00Z | 0 | 1 |  | throttle | 0.00 | 1.50 |
| beta-S4-r2 | 2026-01-07T21:54:00Z to 2026-01-07T22:04:00Z | 0 | 1 |  |  | n/a | n/a |
| beta-S5-r1 | 2026-01-07T22:10:00Z to 2026-01-07T22:22:46Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S4-r1 | 2026-01-08T19:14:00Z to 2026-01-08T19:24:00Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S1-r1 | 2026-01-08T19:30:00Z to 2026-01-08T19:40:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| gamma-S2-r1 | 2026-01-08T19:46:00Z to 2026-01-08T19:56:00Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S1-r2 | 2026-01-08T20:02:00Z to 2026-01-08T20:12:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| gamma-S2-r2 | 2026-01-08T20:18:00Z to 2026-01-08T20:28:00Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S4-r2 | 2026-01-08T20:34:00Z to 2026-01-08T20:44:00Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S2-r3 | 2026-01-08T20:50:00Z to 2026-01-08T21:00:00Z | 3 | 0 | missing-summary |  | n/a | n/a |
| gamma-S4-r3 | 2026-01-08T21:06:00Z to 2026-01-08T21:16:00Z | 0 | 1 |  |  | n/a | n/a |
| gamma-S1-r3 | 2026-01-08T21:22:00Z to 2026-01-08T21:32:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-aa-S4-r1 | 2026-01-09T19:14:00Z to 2026-01-09T19:24:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-aa-S1-r1 | 2026-01-09T19:30:00Z to 2026-01-09T19:40:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-aa-S1-r2 | 2026-01-09T19:46:00Z to 2026-01-09T19:56:00Z | 0 | 1 |  |  | 0.00 | 0.20 |
| alpha-aa-S4-r2 | 2026-01-09T20:02:00Z to 2026-01-09T20:12:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-aa-S4-r3 | 2026-01-09T20:18:00Z to 2026-01-09T20:28:00Z | 0 | 1 |  |  | n/a | n/a |
| alpha-aa-S1-r3 | 2026-01-09T20:34:00Z to 2026-01-09T20:44:00Z | 0 | 1 |  |  | 0.00 | 0.20 |

Data gaps and warnings:

- alpha: Q2 missing for 6 valid rep(s): alpha-S2-r1, alpha-S2-r2, alpha-S4-r2, alpha-S2-r3, alpha-S4-r3, alpha-S5-r1.
- alpha: Q3 missing for 8 valid rep(s): alpha-S4-r1, alpha-S2-r1, alpha-S3-r1, alpha-S2-r2, alpha-S4-r2, alpha-S2-r3, alpha-S4-r3, alpha-S5-r1.
- alpha: Q7 missing for 7 valid rep(s): alpha-S4-r1, alpha-S2-r1, alpha-S2-r2, alpha-S4-r2, alpha-S2-r3, alpha-S4-r3, alpha-S5-r1.
- alpha: Q8 missing for 7 valid rep(s): alpha-S4-r1, alpha-S2-r1, alpha-S2-r2, alpha-S4-r2, alpha-S2-r3, alpha-S4-r3, alpha-S5-r1.
- alpha: Q9 missing for 7 valid rep(s): alpha-S4-r1, alpha-S2-r1, alpha-S2-r2, alpha-S4-r2, alpha-S2-r3, alpha-S4-r3, alpha-S5-r1.
- beta: Q2 missing for 6 valid rep(s): beta-S2-r1, beta-S2-r2, beta-S2-r3, beta-S4-r3, beta-S4-r2, beta-S5-r1.
- beta: Q3 missing for 7 valid rep(s): beta-S4-r1, beta-S2-r1, beta-S2-r2, beta-S2-r3, beta-S4-r3, beta-S4-r2, beta-S5-r1.
- beta: Q7 missing for 7 valid rep(s): beta-S4-r1, beta-S2-r1, beta-S2-r2, beta-S2-r3, beta-S4-r3, beta-S4-r2, beta-S5-r1.
- beta: Q8 missing for 7 valid rep(s): beta-S4-r1, beta-S2-r1, beta-S2-r2, beta-S2-r3, beta-S4-r3, beta-S4-r2, beta-S5-r1.
- beta: Q9 missing for 7 valid rep(s): beta-S4-r1, beta-S2-r1, beta-S2-r2, beta-S2-r3, beta-S4-r3, beta-S4-r2, beta-S5-r1.
- beta: Q18 missing for 1 valid rep(s): beta-S5-r1.
- gamma: Q2 missing for 5 valid rep(s): gamma-S4-r1, gamma-S2-r1, gamma-S2-r2, gamma-S4-r2, gamma-S4-r3.
- gamma: Q3 missing for 8 valid rep(s): gamma-S4-r1, gamma-S1-r1, gamma-S2-r1, gamma-S1-r2, gamma-S2-r2, gamma-S4-r2, gamma-S4-r3, gamma-S1-r3.
- gamma: Q7 missing for 5 valid rep(s): gamma-S4-r1, gamma-S2-r1, gamma-S2-r2, gamma-S4-r2, gamma-S4-r3.
- gamma: Q8 missing for 5 valid rep(s): gamma-S4-r1, gamma-S2-r1, gamma-S2-r2, gamma-S4-r2, gamma-S4-r3.
- gamma: Q9 missing for 5 valid rep(s): gamma-S4-r1, gamma-S2-r1, gamma-S2-r2, gamma-S4-r2, gamma-S4-r3.
- alpha-aa: Q2 missing for 3 valid rep(s): alpha-aa-S4-r1, alpha-aa-S4-r2, alpha-aa-S4-r3.
- alpha-aa: Q3 missing for 6 valid rep(s): alpha-aa-S4-r1, alpha-aa-S1-r1, alpha-aa-S1-r2, alpha-aa-S4-r2, alpha-aa-S4-r3, alpha-aa-S1-r3.
- alpha-aa: Q7 missing for 3 valid rep(s): alpha-aa-S4-r1, alpha-aa-S4-r2, alpha-aa-S4-r3.
- alpha-aa: Q8 missing for 3 valid rep(s): alpha-aa-S4-r1, alpha-aa-S4-r2, alpha-aa-S4-r3.
- alpha-aa: Q9 missing for 3 valid rep(s): alpha-aa-S4-r1, alpha-aa-S4-r2, alpha-aa-S4-r3.

## Covariates per session

Not recorded: add a `covariates` array to series-manifest.json.

## Method & raw data

- Method, controls and statistics: wiki/PERF_VERSION_COMPARISON.md.
- One row per attempted rep: runs.csv, next to this report.
- Raw inputs per label: `windows.tsv`, `k6/<scenario>-r<n>.json` and `nr/<query>-<run_id>.json`.
