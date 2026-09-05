#!/usr/bin/env bash
# scripts/lib/rabbitmq-mgmt.sh
#
# Shared RabbitMQ management-API safety helpers for the sealed-message demo
# scripts (show-sealed-message.sh, seal-event-demo.sh). Both scripts talk to the
# same endpoint with the same credentials, so the guard that decides when that is
# safe and the file that keeps the credentials off argv live here once.
#
# Sourced, never executed. The caller must define fail() before the first call.

# guard_mgmt_endpoint URL — refuse an endpoint that would leak credentials.
#
# Every management call sends RABBIT_USER / RABBIT_PASS. Plaintext HTTP is only
# defensible when the request cannot leave the host, so http:// is accepted for
# loopback and https:// is required for anything else. RABBIT_MGMT keeps working
# as an override — it just has to name one of the two. The authority is matched
# whole so a `user@real-host` form cannot hide the real host behind
# loopback-looking userinfo.
guard_mgmt_endpoint() {
    local url="$1" host
    case "$url" in
        https://*) ;;
        http://*)
            host="${url#http://}"
            host="${host%%/*}"
            [[ "$host" =~ ^(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?$ ]] \
                || fail "RABBIT_MGMT='$url' would send broker credentials in the clear to non-loopback host '$host' — use https://, or a loopback host (localhost, 127.0.0.1, [::1])"
            ;;
        *) fail "RABBIT_MGMT='$url' must start with https://, or http:// for a loopback host (localhost, 127.0.0.1, [::1])" ;;
    esac
}

# write_curl_cfg PATH URL USER PASS — render a 0600 curl config carrying the
# broker credentials, for `curl -K PATH`.
#
# Credentials never reach argv: `curl -u user:pass` is readable by every process
# on the host through ps(1). curl's config parser reads a double-quoted value
# with \" and \\ escapes, so escape backslashes first and then quotes — a
# password containing either would otherwise truncate or corrupt the credential.
#
# For a loopback http:// endpoint (guard_mgmt_endpoint already forbids
# non-loopback http), forbid curl from honoring a stray http_proxy/HTTP_PROXY so
# credentials can never be routed through a proxy.
write_curl_cfg() {
    local path="$1" url="$2" user="$3" pass="$4" esc_user esc_pass
    chmod 600 "$path"
    esc_user="${user//\\/\\\\}"; esc_user="${esc_user//\"/\\\"}"
    esc_pass="${pass//\\/\\\\}"; esc_pass="${esc_pass//\"/\\\"}"
    printf 'user = "%s:%s"\n' "$esc_user" "$esc_pass" >"$path"
    if [[ "$url" == http://* ]]; then printf 'noproxy = "*"\n' >>"$path"; fi
}
