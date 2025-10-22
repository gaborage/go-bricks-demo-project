<!--
Sync Impact Report:
Version change: N/A → v1.0.0 (initial constitution)
Modified principles: N/A (new constitution)
Added sections:
  - Core Principles (9 principles)
  - Testing & Quality Standards
  - Observability Requirements
  - Governance
Removed sections: N/A
Templates requiring updates:
  ✅ plan-template.md - Constitution Check section aligned
  ✅ spec-template.md - Requirements section aligned with framework principles
  ✅ tasks-template.md - Task categories reflect testing and observability requirements
  ⚠️ No command templates found in .specify/templates/commands/ - create if needed
Follow-up TODOs: None
-->

# Go-Bricks Demo Project Constitution

## Core Principles

### I. Framework First

**MUST** reach for shipped bricks (config loader, module wiring, telemetry helpers, secrets store) before inventing bespoke plumbing. Applications built with GoBricks **MUST** leverage the framework's production-grade tooling to maintain rapid development velocity with enterprise quality.

**Rationale**: GoBricks provides battle-tested infrastructure. Custom plumbing introduces maintenance burden and defeats the framework's purpose of enabling fast MVP development.

### II. Explicit > Implicit

Code **MUST** be clear. No hidden defaults, no magic configuration. All behavior **MUST** be explicitly declared and visible in code or configuration files.

**Rationale**: Explicit code is maintainable code. First-time readers should understand system behavior without spelunking through framework internals or guessing at conventions.

### III. Type Safety > Dynamic Hacks

Refactor-friendly code is **REQUIRED**. Breaking changes are prioritized when they improve compile-time safety. Dynamic type assertions, reflection, and runtime tricks **MUST** be justified in code comments.

**Rationale**: Type safety catches errors at compile time rather than production. The framework prioritizes safety over convenience.

### IV. Deterministic > Dynamic Flow

Predictable, testable logic is **MANDATORY**. Same inputs **MUST** always produce same outputs. No hidden global state, no side effects in pure functions.

**Rationale**: Non-deterministic code is untestable and unreliable. Showcase demonstrations must work consistently every time.

### V. Composition > Inheritance

Flexible, simple structures are **REQUIRED**. Use interfaces and embedding over class hierarchies. Services **MUST** compose behavior via dependency injection rather than inheritance chains.

**Rationale**: Go's composition model is simpler and more flexible than inheritance. Following Go idioms makes the codebase accessible to Go developers.

### VI. Security First

- Input validation is **MANDATORY** at all boundaries (HTTP handlers, messaging consumers, database queries)
- `WhereRaw()` usage **REQUIRES** annotation: `// SECURITY: Manual SQL review completed - identifier quoting verified`
- Secrets **MUST** be loaded from environment variables or secret managers (AWS Secrets Manager, HashiCorp Vault)
- Hardcoded credentials are **FORBIDDEN**
- Secrets **MUST NOT** appear in logs or error messages
- Audit logging is **REQUIRED** for sensitive operations (access control changes, data modifications) with trace IDs for correlation

**Rationale**: Security is not optional. The showcase demonstrates production patterns, and production systems must be secure by default.

### VII. Context-First Design

`context.Context` **MUST** be the first parameter of every function that performs I/O, makes network calls, or accesses external resources. No global variables for tenant IDs or trace IDs—always thread context through calls.

**Rationale**: Context enables tracing, cancellation, deadlines, and multi-tenant isolation. GoBricks' observability and multi-tenancy depend on proper context propagation.

### VIII. Interface Segregation

Small, focused interfaces are **REQUIRED** for testability. Prefer narrow interfaces (e.g., `Client` vs `AMQPClient`) that define only the methods a consumer needs.

**Rationale**: Small interfaces make mocking easier and reduce coupling. Services should depend on minimal contracts.

### IX. Vendor Agnosticism

Abstract high-cost dependencies (databases, cloud providers). Embrace low-cost dependencies (HTTP frameworks, logging libraries). Database access **MUST** use GoBricks' query builder abstraction to support multiple backends.

**Rationale**: Vendor lock-in increases switching costs. The framework demonstrates vendor-neutral patterns that translate across deployments.

## Testing & Quality Standards

### Coverage Target

60-70% code coverage on core business logic (repository queries, service methods, HTTP handlers). Framework code and boilerplate excluded.

### Testing Focus

- **ALWAYS TEST**: Database queries, HTTP handlers, messaging consumers
- **Happy paths** + critical error scenarios (validation failures, DB errors, not found cases)
- **Demo coverage**: Each showcased brick (telemetry spans, repository queries, scheduled jobs, secrets handling) **MUST** have at least one runnable integration or acceptance example
- **Defer**: Exotic configuration combinations, rare edge cases
- **Iterate**: Code may be refactored as requirements evolve while refining the demo

### Quality Gate

**MANDATORY** before merging to main:

1. `make fmt` - Code formatting with gofmt (zero errors)
2. `make lint` - Static analysis with golangci-lint (zero errors)
3. `make test` - All tests pass with race detector enabled

### Recommended Checks

- `make coverage` - Review HTML coverage report, aim for 60-70% on business logic
- Integration tests - Add or update when introducing new database queries, HTTP endpoints, or messaging flows
- Load tests - Run `make loadtest-smoke` to validate performance hasn't regressed

## Observability Requirements

### OpenTelemetry Standards

All modules **MUST** emit OpenTelemetry traces and metrics:

- HTTP handlers automatically instrumented via framework middleware
- Database queries automatically traced via repository layer
- Custom business logic **SHOULD** add spans for operations >10ms
- Metrics **MUST** use `gobricks_` namespace prefix

### W3C Traceparent Propagation

Trace context **MUST** propagate across HTTP and messaging boundaries using W3C traceparent headers. Multi-hop request chains **MUST** maintain trace continuity.

### Logging Standards

- Structured logging via `zerolog` with trace IDs
- Log levels: ERROR (action required), WARN (notable events), INFO (key operations), DEBUG (verbose tracing)
- No secrets in logs
- Correlation: Every log entry **MUST** include trace_id when available

### Metrics Exposure

Application **MUST** expose Prometheus-compatible metrics on `/metrics` endpoint for scraping by observability stack (local or cloud).

## Governance

### Amendment Process

1. **Proposal**: Open GitHub issue describing principle addition/change with rationale
2. **Discussion**: Maintainers discuss impact on existing code and showcase goals
3. **Approval**: Requires maintainer consensus
4. **Migration Plan**: For breaking changes, document migration path for existing features
5. **Update**: Increment constitution version and update dependent templates

### Versioning Policy

Constitution follows semantic versioning:

- **MAJOR**: Backward incompatible governance/principle removals or redefinitions
- **MINOR**: New principle/section added or materially expanded guidance
- **PATCH**: Clarifications, wording, typo fixes, non-semantic refinements

### Compliance Review

All pull requests **MUST** verify compliance with constitution principles. Reviewers **SHOULD** reference specific principles when requesting changes. Complexity (e.g., new abstractions, additional dependencies) **MUST** be justified against KISS and YAGNI principles.

### Runtime Guidance

For implementation details not covered here, consult:

- [CLAUDE.md](../../CLAUDE.md) - Complete developer guide (AI agent-specific)
- [wiki/developer.manifesto.md](../../wiki/developer.manifesto.md) - Detailed engineering principles
- [README.md](../../README.md) - Quick start and architecture overview

**Version**: v1.0.0 | **Ratified**: 2025-10-21 | **Last Amended**: 2025-10-21
