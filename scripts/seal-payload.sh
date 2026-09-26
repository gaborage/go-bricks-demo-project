#!/usr/bin/env bash
# scripts/seal-payload.sh
#
# JOSE request bodies for the tokens demo, minted with the framework's
# `seal-payload` CLI (go-bricks v0.65.0, #1615/#1620). Driven by
# `make seal-payload` (nested) and `make seal-mle` (mle).
#
# Reads a JSON payload on stdin and prints ONLY the sealed body on stdout, so
# the output pipes straight into `curl --data-binary @-`. Everything else
# (usage, missing keys, `go: downloading ...`) goes to stderr.
#
#   nested  JWE(JWS(payload)) for POST /api/v1/tokens. Plays the PEER: signs
#           with the peer PRIVATE key (kid tokens-peer, the route's verify=
#           name) and encrypts to our PUBLIC key (kid tokens-our, its decrypt=
#           name) — the same keys and kids the retired cmd/seal-payload used.
#
#   mle     Visa Message Level Encryption for POST /api/v1/__sim/peer/mle: one
#           bare JWE (no inner JWS; RSA-OAEP-256 + A128GCM, typ JOSE,
#           millisecond iat) inside {"encData":"<compact>"}. Plays the RELAY's
#           outbound role: encrypts to the peer PUBLIC key (kid tokens-peer),
#           which is the key the MLE peer simulator opens with. Nothing is
#           signed — bare mode authenticates no sender (go-bricks ADR-107).
#
# Kids, key files and the MLE header shape must match internal/modules/tokens:
# the OurKid/PeerKid constants in module.go, the jose: tags in
# handlers/handlers.go and NewMLEOutboundPolicy in service/mle_relay_service.go.
# A drifted kid fails server-side as JOSE_KID_UNKNOWN.
#
# The CLI version is read from go.mod, so the sealer is the framework release
# the app links and there is no second pin to drift. SEAL_PAYLOAD_VERSION
# overrides it only to try another release.
#
# The CLI only SEALS. The replies (a nested compact from /tokens, an encData
# envelope from the MLE simulator) are sealed back to us and stay opaque here;
# the relay endpoints are the path that opens them for you.
#
# DEMO DATA ONLY. Feed the documented network test PANs the README uses, never
# a real cardholder number. This script never prints its input.
#
# Prerequisites:
#   make generate-keys  # certs/tokens_{our,peer}_{public,private}.der
#
# Usage:
#   <json on stdin> | scripts/seal-payload.sh nested|mle
#
# Overrides (env): SEAL_PAYLOAD_VERSION

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

FRAMEWORK_MODULE="github.com/gaborage/go-bricks"

# Kid names — must match internal/modules/tokens/module.go (OurKid / PeerKid).
OUR_KID="tokens-our"
PEER_KID="tokens-peer"

# Key files written by `make generate-keys`. The peer public key is also
# inlined into config.development.yaml (the keystore's value: source); this is
# the same key on disk.
OUR_PUBLIC_KEY="certs/tokens_our_public.der"
PEER_PRIVATE_KEY="certs/tokens_peer_private.der"
PEER_PUBLIC_KEY="certs/tokens_peer_public.der"

# Visa MLE header shape — mirrors NewMLEOutboundPolicy.
MLE_ENC="A128GCM"
MLE_TYP="JOSE"

fail() {
    echo "❌ $*" >&2
    exit 1
}

usage() {
    echo "usage: <json on stdin> | $0 nested|mle" >&2
    echo "  nested  JWE-of-JWS body for POST /api/v1/tokens (make seal-payload)" >&2
    echo "  mle     {\"encData\":...} body for POST /api/v1/__sim/peer/mle (make seal-mle)" >&2
    exit 2
}

require_key() {
    [ -f "$1" ] || fail "$1 not found — run 'make generate-keys' first"
}

[ "$#" -eq 1 ] || usage
MODE="$1"
case "$MODE" in
    nested|mle) ;;
    *) usage ;;
esac

command -v go >/dev/null 2>&1 || fail "go is required but not installed"

# Without a pipe the CLI would sit waiting on the terminal.
if [ -t 0 ]; then
    echo "❌ no payload on stdin — pipe a JSON body in (see README, Tokens walkthrough)" >&2
    usage
fi

# An untracked go.work pointing at a sibling framework checkout would make
# `go list` report the workspace module, which has no version. Resolve against
# go.mod, exactly as CI does, and keep the sealer itself off the workspace too.
export GOWORK=off

if [ -z "${SEAL_PAYLOAD_VERSION:-}" ]; then
    SEAL_PAYLOAD_VERSION="$(go list -m -f '{{.Version}}' "$FRAMEWORK_MODULE")" \
        || fail "could not read the $FRAMEWORK_MODULE version from go.mod"
fi
[ -n "$SEAL_PAYLOAD_VERSION" ] || fail "go.mod reports no version for $FRAMEWORK_MODULE"
SEAL_PAYLOAD_PKG="${FRAMEWORK_MODULE}/cmd/seal-payload@${SEAL_PAYLOAD_VERSION}"

case "$MODE" in
    nested)
        require_key "$PEER_PRIVATE_KEY"
        require_key "$OUR_PUBLIC_KEY"
        exec go run "$SEAL_PAYLOAD_PKG" \
            -sign-key-file "$PEER_PRIVATE_KEY" -sign-kid "$PEER_KID" \
            -encrypt-key-file "$OUR_PUBLIC_KEY" -encrypt-kid "$OUR_KID"
        ;;
    mle)
        require_key "$PEER_PUBLIC_KEY"
        exec go run "$SEAL_PAYLOAD_PKG" -mode bare \
            -encrypt-key-file "$PEER_PUBLIC_KEY" -encrypt-kid "$PEER_KID" \
            -enc "$MLE_ENC" -typ "$MLE_TYP" -iat-ms \
            -envelope visa-mle
        ;;
esac
