# Developer Manifesto (MANDATORY)

## Framework Philosophy
GoBricks is a **production-grade framework for building MVPs fast**. It provides enterprise-quality tooling (validation, observability, tracing, type safety) while enabling rapid development velocity. The framework itself maintains high quality standards so applications built with it can move quickly with confidence.

This repository is the public GoBricks showcase. It exists so external engineers can clone the project, run it locally, and experience core bricks—configuration, observability, secrets, jobs, messaging—without reverse engineering. Every contribution should sharpen that first-hour experience.

## Core Principles
- **Framework First** → Reach for shipped bricks (config loader, module wiring, telemetry helpers, secrets store) before inventing bespoke plumbing.
- **Explicit > Implicit** → Code must be clear. No hidden defaults, no magic configuration.
- **Type Safety > Dynamic Hacks** → Refactor-friendly code. Breaking changes prioritized for compile-time safety.
- **Deterministic > Dynamic Flow** → Predictable, testable logic. Same inputs always produce same outputs.
- **Composition > Inheritance** → Flexible, simple structures. Use interfaces and embedding over inheritance.
- **Robustness** → Handle errors idiomatically, wrap once at boundaries. No silent failures.
- **Patterns, not Over-Design** → Use them only when they solve real problems. Justify abstractions.
- **Security First** → Input validation mandatory, secrets from env/vault, audit `WhereRaw()` usage.
- **Context-First Design** → Always pass `context.Context` as first parameter for tracing, cancellation, deadlines.
- **Interface Segregation** → Small, focused interfaces for testability (e.g., `Client` vs `AMQPClient`).
- **Vendor Agnosticism** → Abstract high-cost dependencies (databases), embrace low-cost ones (HTTP frameworks).

### Detailed Security Guidelines
- Input validation is **mandatory** at all boundaries (HTTP, messaging, database)
- `WhereRaw()` requires annotation: `// SECURITY: Manual SQL review completed - identifier quoting verified`
- Secrets from environment variables or secret managers (AWS Secrets Manager, HashiCorp Vault)
- No hardcoded credentials, no secrets in logs or error messages
- Audit logging for sensitive operations (access control, data modifications)

## Practices & Patterns
- **SOLID** → Encapsulate behavior behind narrow interfaces (see `internal/modules/products/repository/repository.go`) so services remain testable and swappable.
- **Fail Fast** → Abort startup when initialization misbehaves (`cmd/api/main.go` uses fatal logging for module registration failures).
- **DRY** → Share cross-cutting capabilities via bricks in `internal/modules/shared` instead of copy-pasting helpers.
- **CQS** → Split reads and writes where clarity improves (`internal/modules/products/http` handlers call query and command-specific service methods).
- **KISS** → Prefer the defaults that GoBricks provides before layering additional frameworks or wrappers.
- **YAGNI** → Only build flows the showcase actively demonstrates today; defer speculative features to ADRs before investing.

### YAGNI Exceptions
- Abstractions for **vendor differences** (databases, cloud providers) are justified
- Test utilities justified **only if actively used** (measure utility function calls)
- Breaking changes acceptable when justified for safety/correctness (see ADRs)

## Framework vs. Application Development

**Applications Built with GoBricks:**
- **Coverage Target:** 60-70% on core business logic
- **Testing Focus:** Happy paths + critical error scenarios
- **Always Test:** Database queries, HTTP handlers, messaging consumers
- **Demo Coverage:** Each showcased brick (telemetry spans, repository queries, scheduled jobs, secrets handling) has at least one runnable integration or acceptance example.
- **Defer:** Exotic configuration combinations, rare edge cases
- **Iterate:** Expect some code to be throwaway/refactored as requirements evolve while we refine the demo

## Onboarding Workflow
- **Bootstrap:** `make check-deps` then `make dev` to spin up PostgreSQL, RabbitMQ, and apply migrations.
- **Run the API:** `make run`, then exercise health and product endpoints (`scripts/test-products-api.sh`).
- **Inspect Telemetry:** Follow `wiki/PROMETHEUS_GRAFANA_SETUP.md` to explore metrics and traces emitted by the demo.
- **Quality Gate:** Before pushing, run `make check` (fmt + lint + tests) so visitors always land on a green main branch.

## Engineering Principles (Go & Architecture Mindset)
- **Observability:** OpenTelemetry standards, W3C traceparent propagation across HTTP/messaging
- **12-Factor App:** Environment variables for config, stateless design, explicit dependencies
- **Error Handling:** Idiomatic Go errors (`fmt.Errorf`, `errors.Is/As`), structured errors at API boundaries
- **Context Propagation:** No global variables for tenant IDs or trace IDs—always thread context through calls
- **Automation:** Prefer the provided Makefile targets (`make lint`, `make test`, `make coverage`, `make docker-up`) to keep the showcase reproducible across platforms.
- **Documentation:** Just enough for others to understand quickly, examples over exhaustive docs

## Showcase Playbook
- **Code Tour:** Start at `cmd/api/main.go`, then explore module wiring in `internal/modules/products/module.go`, HTTP handlers in `internal/modules/products/http`, the repository implementation in `internal/modules/products/repository`, and shared bricks under `internal/modules/shared`.
- **Runtime Tour:** Use `make dev` + `make run`, hit the sample endpoints, review emitted traces/logs, and inspect generated metrics/dashboards to see GoBricks observability out of the box.

## Contribution Workflow
- **Plan Updates:** Capture framework-impacting changes in ADRs or the `wiki/` directory so first-time readers see the latest guidance.
- **Keep the Demo Fresh:** When you extend functionality, add example requests, scripts, or docs showing how to experience it.
- **Validate:** Run `make fmt`, `make lint`, and `make test` locally; add or update integration tests when you introduce new bricks or flows.
- **Document Touchpoints:** Update relevant README snippets, sample `.env`, and onboarding steps whenever configuration or dependencies change.

## What Success Looks Like
Visitors should be able to say, “I stood up a tenant-aware API with tracing, secrets, jobs, and database access in under an hour using GoBricks,” and they should leave the repo confident they can repeat that pattern in their own domain.

**"Build it simple, build it strong, and refactor when it matters."**
