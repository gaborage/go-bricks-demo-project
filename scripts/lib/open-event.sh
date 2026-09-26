#!/usr/bin/env bash
# scripts/lib/open-event.sh
#
# Shared runner for the framework's `open-event` CLI (go-bricks v0.65.0, #1640),
# sourced by the sealed-message demo scripts (show-sealed-message.sh,
# seal-event-demo.sh). open-event verifies and decrypts one sealed body through
# the production sealed.OpenDocument path (#1633) — the same open rules, in the
# same order, with the same SEAL_* codes as the payments consumer — so a script
# can show the CONSUMER's verdict on bytes read off the broker, without the app
# or its log.
#
# It holds the consumer role: its key flags take the sign PUBLIC half (verify)
# and the encrypt PRIVATE half (decrypt), the mirror of seal-event's. Both wire
# kids are required flags and are never read from the unauthenticated header.
#
# Sourced, never executed. The caller must define fail() before the first call.

# install_open_event VERSION DIR — build the CLI at VERSION into DIR and point
# OPEN_EVENT_BIN at it.
#
# Installed, not `go run`: go run reports ANY non-zero exit of the program it ran
# as its own exit 1 (plus an "exit status N" line on stderr), so a refusal (3)
# would be indistinguishable from a tool error (1) — and the refusal exit is
# exactly what these scripts assert. `go install pkg@version` ignores the current
# module and any go.work, so VERSION alone decides what runs.
install_open_event() {
    local version="$1" dir="$2"
    GOBIN="$dir" go install "github.com/gaborage/go-bricks/cmd/open-event@${version}" \
        || fail "could not install open-event@${version} — check network access to the Go module proxy"
    OPEN_EVENT_BIN="$dir/open-event"
    [[ -x "$OPEN_EVENT_BIN" ]] || fail "open-event@${version} installed no binary at $OPEN_EVENT_BIN"
}

# open_event ARGS... — run the installed CLI with ARGS; its exit status is the
# CLI's own: 0 opened, 1 tool error, 2 usage, 3 refused.
#
# NEVER pass -print-subject. It splices the DECRYPTED subject — the card, PAN
# included — into stdout; the CLI documents it as a fixture-only escape hatch.
# These scripts rely on the default instead, which renders the subject member as
# the fixed literal "<redacted>" (no plaintext, no length hint), so the flag is
# refused here in every spelling Go's flag package accepts rather than merely
# left out.
open_event() {
    local arg
    [[ -n "${OPEN_EVENT_BIN:-}" ]] || fail "open_event called before install_open_event"
    for arg in "$@"; do
        case "$arg" in
            -print-subject* | --print-subject*)
                fail "open_event: refusing -print-subject — it would print the decrypted card (PAN)" ;;
        esac
    done
    "$OPEN_EVENT_BIN" "$@"
}

# assert_redacted PAN LABEL FILE... — fail if the PAN appears in any FILE,
# WITHOUT printing a byte of it.
#
# open-event already renders the subject as "<redacted>"; this is the independent
# check a script runs on the CLI's captured stdout and stderr BEFORE echoing
# either, so a regression in the tool can never put the card on a terminal or a
# CI log. An empty PAN is refused: grep -F with an empty pattern matches
# everything, and a check that cannot fail proves nothing.
assert_redacted() {
    local pan="$1" label="$2" file
    shift 2
    [[ -n "$pan" ]] || fail "assert_redacted: no PAN to check $label against"
    for file in "$@"; do
        grep -qF -- "$pan" "$file" \
            && fail "PAN FOUND IN $label — not printing it; the subject was not redacted"
    done
    return 0
}
