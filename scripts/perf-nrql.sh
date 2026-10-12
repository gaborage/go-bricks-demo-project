#!/usr/bin/env bash
# scripts/perf-nrql.sh
#
# New Relic NerdGraph client for the perf harness. Three modes:
#
#   --graphql QUERY          one raw GraphQL request; response JSON on stdout
#   --query NRQL             one NRQL query against --account; response on stdout
#   --queries YAML --windows TSV --vars "K=V,..." --out DIR [--trim 1m]
#                            every query in YAML (loadtests/nrql/queries.yaml) for
#                            every valid row of a windows.tsv; each raw response
#                            goes to <out>/<id>-<run_id>.json, session-scope
#                            queries to <out>/<id>-session.json
#   add --print to the batch mode to print "<id> TAB <run_id> TAB <nrql>" lines
#   instead of calling New Relic (no key needed): the rendered text is the whole
#   contract, so it is golden-tested and diffable across labels.
#
# The User key. It is read from the macOS Keychain
# (`security find-generic-password -a "$USER" -s nr-user-key -w`), or from the
# first line of stdin with --key-stdin. It lives in one unexported shell variable
# and reaches curl only through `--config -` on stdin, written by the printf
# builtin: it is never in any process's argv, never printed and never written to
# a file. The script refuses to start when xtrace (set -x) is on, because a trace
# would print it. Store it once with:
#   security add-generic-password -a "$USER" -s nr-user-key -w
#
# Placeholders: see the header of loadtests/nrql/queries.yaml. Values come from
# PERF_NRQL_VARS, overridden by --vars; S=<name> is accepted as an alias for
# SERVICE=<name>. {S}, {R} and {W} are always derived and cannot be set.
#
# Exit codes: 0 every request succeeded, 1 a request failed (HTTP error or a
# GraphQL `errors` member; batch mode carries on and writes the response to
# <file>.err), 2 usage (including an unresolved placeholder, caught before any
# request is sent).

case "$-" in *x*) echo "perf-nrql: refusing to run with xtrace on: a trace would print the New Relic key" >&2; exit 2 ;; esac
case ":${SHELLOPTS:-}:" in *:xtrace:*) echo "perf-nrql: refusing to run with xtrace in SHELLOPTS" >&2; exit 2 ;; esac

set -euo pipefail
shopt -u patsub_replacement 2>/dev/null || true   # bash 5.2+: '&' in a replacement stays literal

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=lib/perf-common.sh
source "$SCRIPT_DIR/lib/perf-common.sh"

log() { echo "perf-nrql: $*" >&2; }
die() { log "error: $*"; exit 1; }
usage_err() { log "$*"; log "run with --help for usage"; exit 2; }

usage() {
    cat <<'EOF'
Usage:
  perf-nrql.sh --graphql QUERY [--region US|EU] [--key-stdin]
  perf-nrql.sh --query NRQL [--account ID] [--region US|EU] [--key-stdin]
  perf-nrql.sh --queries YAML --windows TSV --vars "K=V,..." --out DIR
               [--trim DUR] [--account ID] [--region US|EU] [--key-stdin] [--print]
  perf-nrql.sh --queries YAML --list     print the parsed query table and exit

  --account defaults to NEW_RELIC_ACCOUNT_ID. --region defaults to US.
  --trim (default 1m) cuts each end of a window before flooring to whole minutes.
  Only windows.tsv rows with valid=1 are queried.
EOF
}

opt_graphql="" opt_query="" opt_queries="" opt_windows="" opt_vars="" opt_out="" opt_trim="1m"
opt_account="${NEW_RELIC_ACCOUNT_ID:-}" opt_region="US" opt_key_stdin=0 opt_print=0 opt_list=0

need_value() { [[ $# -ge 2 && -n "${2-}" ]] || usage_err "$1 needs a value"; }
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --graphql) need_value "$@"; opt_graphql="$2"; shift 2 ;;
        --query) need_value "$@"; opt_query="$2"; shift 2 ;;
        --queries) need_value "$@"; opt_queries="$2"; shift 2 ;;
        --windows) need_value "$@"; opt_windows="$2"; shift 2 ;;
        --vars) need_value "$@"; opt_vars="$2"; shift 2 ;;
        --out) need_value "$@"; opt_out="$2"; shift 2 ;;
        --trim) need_value "$@"; opt_trim="$2"; shift 2 ;;
        --account) need_value "$@"; opt_account="$2"; shift 2 ;;
        --region) need_value "$@"; opt_region="$2"; shift 2 ;;
        --key-stdin) opt_key_stdin=1; shift ;;
        --print) opt_print=1; shift ;;
        --list) opt_list=1; shift ;;
        *) usage_err "unknown argument: $1" ;;
    esac
done

modes=0
[[ -z "$opt_graphql" ]] || modes=$((modes + 1))
[[ -z "$opt_query" ]] || modes=$((modes + 1))
[[ -z "$opt_queries" ]] || modes=$((modes + 1))
[[ $modes -eq 1 ]] || usage_err "pick exactly one of --graphql, --query, --queries"
[[ $opt_print -eq 0 || -n "$opt_queries" ]] || usage_err "--print only applies to --queries"
[[ $opt_list -eq 0 || -n "$opt_queries" ]] || usage_err "--list only applies to --queries"
case "$opt_region" in
    US) ENDPOINT="https://api.newrelic.com/graphql" ;;
    EU) ENDPOINT="https://api.eu.newrelic.com/graphql" ;;
    *) usage_err "--region must be US or EU" ;;
esac
command -v jq >/dev/null 2>&1 || die "jq is required"

# --------------------------------------------------------------------------
# Key and transport
# --------------------------------------------------------------------------

nr_key=""
read_key() {
    if [[ $opt_key_stdin -eq 1 ]]; then
        IFS= read -r nr_key || true
    else
        command -v security >/dev/null 2>&1 || die "the macOS Keychain (security) is not available; pass the key with --key-stdin"
        nr_key="$(security find-generic-password -a "$USER" -s nr-user-key -w 2>/dev/null)" \
            || die "no nr-user-key item in the Keychain for $USER (see the header of this script)"
    fi
    [[ -n "$nr_key" ]] || die "the New Relic key is empty"
    # curl's config syntax: a double-quoted value. Refuse what it would need escaped.
    [[ "$nr_key" =~ ^[A-Za-z0-9_.-]+$ ]] || die "the New Relic key has unexpected characters"
}

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/perf-nrql.XXXXXX")"
chmod 700 "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT

# nerdgraph BODY_FILE RESPONSE_FILE -> 0 on HTTP 200 without GraphQL errors
nerdgraph() {
    local body="$1" resp="$2" code
    command -v curl >/dev/null 2>&1 || die "curl is required"
    # printf is a builtin: the key never appears in a process argument list.
    if ! code="$(printf 'header = "API-Key: %s"\n' "$nr_key" | curl --silent --show-error \
        --max-time 180 --config - --url "$ENDPOINT" --header 'Content-Type: application/json' \
        --data-binary "@$body" --output "$resp" --write-out '%{http_code}')"; then
        log "request failed (curl)"
        return 1
    fi
    if [[ "$code" != 200 ]]; then
        log "HTTP $code from NerdGraph"
        return 1
    fi
    if ! jq -e 'type == "object"' "$resp" >/dev/null 2>&1; then
        log "the response is not a JSON object"
        return 1
    fi
    if jq -e '(.errors // []) | length > 0' "$resp" >/dev/null; then
        log "GraphQL errors: $(jq -c '[.errors[].message]' "$resp")"
        return 1
    fi
}

# shellcheck disable=SC2016 # GraphQL variables, not shell
NRQL_GRAPHQL='query($accountId: Int!, $nrql: Nrql!) { actor { account(id: $accountId) { nrql(query: $nrql, timeout: 120) { results metadata { eventTypes facets messages timeWindow { begin end } } } } } }'

nrql_body() { # NRQL BODY_FILE
    jq -cn --argjson acct "$opt_account" --arg q "$1" --arg gql "$NRQL_GRAPHQL" \
        '{ query: $gql, variables: { accountId: $acct, nrql: $q } }' >"$2"
}

require_account() {
    [[ -n "$opt_account" ]] || usage_err "--account or NEW_RELIC_ACCOUNT_ID is required"
    [[ "$opt_account" =~ ^[0-9]+$ ]] || usage_err "the account id must be numeric"
}

# --------------------------------------------------------------------------
# Single requests
# --------------------------------------------------------------------------

if [[ -n "$opt_graphql" ]]; then
    read_key
    jq -cn --arg q "$opt_graphql" '{ query: $q }' >"$TMP_DIR/body.json"
    rc=0
    nerdgraph "$TMP_DIR/body.json" "$TMP_DIR/resp.json" || rc=1
    [[ ! -s "$TMP_DIR/resp.json" ]] || cat "$TMP_DIR/resp.json"
    exit $rc
fi

if [[ -n "$opt_query" ]]; then
    require_account
    read_key
    nrql_body "$opt_query" "$TMP_DIR/body.json"
    rc=0
    nerdgraph "$TMP_DIR/body.json" "$TMP_DIR/resp.json" || rc=1
    [[ ! -s "$TMP_DIR/resp.json" ]] || cat "$TMP_DIR/resp.json"
    exit $rc
fi

# --------------------------------------------------------------------------
# Batch: queries.yaml x windows.tsv
# --------------------------------------------------------------------------

[[ -f "$opt_queries" ]] || usage_err "--queries file not found: $opt_queries"
if [[ $opt_list -eq 0 ]]; then
    [[ -n "$opt_windows" ]] || usage_err "--windows is required"
    [[ -f "$opt_windows" ]] || usage_err "--windows file not found: $opt_windows"
    [[ $opt_print -eq 1 || -n "$opt_out" ]] || usage_err "--out is required"
fi
trim_s="$(duration_s "$opt_trim")" || usage_err "--trim is not a duration: $opt_trim"

# Parse the strict YAML subset into "D <tab> name <tab> template" and
# "Q <tab> id <tab> scope <tab> scenarios <tab> when <tab> nrql" lines.
parse_queries() {
    awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function unq(s) { s = trim(s); if (s ~ /^".*"$/) s = substr(s, 2, length(s) - 2); return s }
    function bad(msg) { printf "queries.yaml line %d: %s\n", NR, msg > "/dev/stderr"; failed = 1; exit 1 }
    function flush() {
        if (id != "") {
            if (nrql == "") bad("query " id " has no nrql")
            printf "Q\t%s\t%s\t%s\t%s\t%s\n", id, scope, scen, when, nrql
        }
        id = ""; scope = "window"; scen = "all"; when = "-"; nrql = ""
    }
    BEGIN { section = ""; block = ""; id = "" }
    /^[ \t]*#/ { next }
    /^[ \t]*$/ { block = ""; next }
    {
        line = $0
        match(line, /^ */); ind = RLENGTH
        if (block != "") {
            if (ind >= 6) { t = trim(line); if (block == "nrql") nrql = (nrql == "" ? t : nrql " " t); next }
            block = ""
        }
        if (ind == 0) {
            if (line ~ /^version:/) next
            if (line ~ /^derived:[ \t]*$/) { section = "derived"; next }
            if (line ~ /^queries:[ \t]*$/) { flush(); section = "queries"; next }
            bad("unexpected top-level line")
        }
        if (section == "derived" && line ~ /^  [A-Z]+: /) {
            k = line; sub(/^  /, "", k); sub(/:.*/, "", k)
            v = line; sub(/^  [A-Z]+: /, "", v)
            printf "D\t%s\t%s\n", k, unq(v); next
        }
        if (section == "queries" && line ~ /^  - id: /) { flush(); id = unq(substr(line, 9)); next }
        if (section == "queries" && id != "" && line ~ /^    [a-z_]+:/) {
            k = line; sub(/^    /, "", k); sub(/:.*/, "", k)
            v = line; sub(/^    [a-z_]+:/, "", v); v = trim(v)
            if (v == ">-") { block = k; next }
            if (k == "scope") scope = unq(v)
            else if (k == "scenarios") scen = unq(v)
            else if (k == "when") when = unq(v)
            else if (k == "nrql") nrql = unq(v)
            else if (k != "purpose" && k != "notes") bad("unknown key " k)
            next
        }
        bad("unexpected line")
    }
    END { if (!failed) flush() }
    ' "$1"
}

parsed="$(parse_queries "$opt_queries")" || usage_err "could not parse $opt_queries"
if [[ $opt_list -eq 1 ]]; then printf '%s\n' "$parsed"; exit 0; fi
DERIVED_S="" DERIVED_R=""
Q_IDS=() Q_SCOPES=() Q_SCENS=() Q_WHENS=() Q_NRQLS=()
while IFS=$'\t' read -r kind a b c d e; do
    case "$kind" in
        D)
            case "$a" in
                S) DERIVED_S="$b" ;;
                R) DERIVED_R="$b" ;;
                *) usage_err "unknown derived placeholder {$a} in $opt_queries" ;;
            esac ;;
        Q)
            [[ "$a" =~ ^Q[0-9]+[a-z]?$ ]] || usage_err "bad query id: $a"
            [[ " ${Q_IDS[*]+"${Q_IDS[*]}"} " != *" $a "* ]] || usage_err "duplicate query id: $a"
            [[ "$b" == window || "$b" == session ]] || usage_err "$a: scope must be window or session"
            Q_IDS+=("$a"); Q_SCOPES+=("$b"); Q_SCENS+=("$c"); Q_WHENS+=("$d"); Q_NRQLS+=("$e") ;;
    esac
done <<<"$parsed"
[[ -n "$DERIVED_S" && -n "$DERIVED_R" ]] || usage_err "$opt_queries must define derived S and R"
[[ ${#Q_IDS[@]} -gt 0 ]] || usage_err "no queries in $opt_queries"

# Placeholder values: PERF_NRQL_VARS, then --vars (a later key wins).
V_NAMES=() V_VALUES=()
set_var() {
    local i
    for ((i = 0; i < ${#V_NAMES[@]}; i++)); do
        if [[ "${V_NAMES[$i]}" == "$1" ]]; then V_VALUES[i]="$2"; return 0; fi
    done
    V_NAMES+=("$1"); V_VALUES+=("$2")
}
get_var() { # NAME -> value; returns 1 when unset
    local i
    for ((i = 0; i < ${#V_NAMES[@]}; i++)); do
        if [[ "${V_NAMES[$i]}" == "$1" ]]; then printf '%s' "${V_VALUES[$i]}"; return 0; fi
    done
    return 1
}
load_vars() { # "K=V,K=V" SOURCE
    local pair k v
    [[ -n "$1" ]] || return 0
    while IFS= read -r pair; do
        [[ -n "$pair" ]] || continue
        [[ "$pair" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || usage_err "$2: expected K=V, got '$pair'"
        k="${BASH_REMATCH[1]}"; v="${BASH_REMATCH[2]}"
        case "$k" in
            R|W) usage_err "$2: {$k} is derived and cannot be set" ;;
            S) k=SERVICE ;;   # alias: S=<service name>
        esac
        case "$k" in
            CNS|CCT|CPOD|CNODE)
                [[ "$v" =~ ^[A-Za-z0-9_.]+$ || "$v" =~ ^\`[A-Za-z0-9_.:/-]+\`$ ]] \
                    || usage_err "$2: $k must be an attribute name (letters, digits, _ and ., or backtick-quoted)" ;;
            ROUTE_PREFIX)
                [[ "$v" =~ ^[A-Za-z0-9_./-]*$ ]] || usage_err "$2: ROUTE_PREFIX has unexpected characters" ;;
            *)
                [[ "$v" =~ ^[A-Za-z0-9_./:@+-]+$ ]] || usage_err "$2: $k must be non-empty [A-Za-z0-9_./:@+-] (no quotes or braces)" ;;
        esac
        set_var "$k" "$v"
    done < <(printf '%s\n' "$1" | tr ',' '\n')
}
load_vars "${PERF_NRQL_VARS:-}" PERF_NRQL_VARS
load_vars "$opt_vars" --vars

# render TEMPLATE WINDOW -> the substituted NRQL; returns 1 (names printed) on leftovers
render() {
    local q="$1" i rest name missing=""
    q="${q//\{S\}/$DERIVED_S}"
    q="${q//\{R\}/$DERIVED_R}"
    q="${q//\{W\}/$2}"
    for ((i = 0; i < ${#V_NAMES[@]}; i++)); do
        q="${q//\{${V_NAMES[$i]}\}/${V_VALUES[$i]}}"
    done
    rest="$q"
    while [[ "$rest" =~ \{([A-Z_][A-Z0-9_]*)\} ]]; do
        name="${BASH_REMATCH[1]}"
        [[ " $missing " == *" $name "* ]] || missing="$missing $name"
        rest="${rest/\{$name\}/}"
    done
    if [[ -n "$missing" ]]; then printf '%s' "${missing# }"; return 1; fi
    printf '%s' "$q"
}

# Windows: valid rows only, with both timestamps; {W} floors to whole minutes.
# Empty TSV fields become "-" first: a tab IFS would collapse them.
W_RUN_IDS=() W_SCENS=() W_CLAUSES=()
while IFS=$'\t' read -r run_id scenario rep warm_start run_start run_end rc valid reason; do
    [[ "$run_id" != run_id && "$run_id" != "-" ]] || continue
    : "$rep" "$warm_start" "$rc"
    if [[ "$valid" != 1 ]]; then log "skipping $run_id: not valid ($reason)"; continue; fi
    if ! is_iso "$run_start" || ! is_iso "$run_end"; then log "skipping $run_id: no run window"; continue; fi
    start_s="$(iso_to_epoch "$run_start")"; end_s="$(iso_to_epoch "$run_end")"
    since_s=$(((start_s + trim_s) / 60 * 60)); until_s=$(((end_s - trim_s) / 60 * 60))
    if ((until_s <= since_s)); then log "skipping $run_id: the window is empty after a ${trim_s}s trim"; continue; fi
    W_RUN_IDS+=("$run_id"); W_SCENS+=("$scenario")
    W_CLAUSES+=("SINCE $((since_s * 1000)) UNTIL $((until_s * 1000))")
done < <(awk -F'\t' 'BEGIN { OFS = "\t" } NF > 0 { for (i = 1; i <= 9; i++) if ($i == "") $i = "-"; print $1, $2, $3, $4, $5, $6, $7, $8, $9 }' "$opt_windows")

# Plan every request first, so an unresolved placeholder stops the run before
# anything is sent.
P_IDS=() P_RUNS=() P_NRQLS=()
plan() { # ID RUN_ID TEMPLATE WINDOW
    local out
    if ! out="$(render "$3" "$4")"; then
        usage_err "$1: unresolved placeholder(s): $out (set them in --vars or PERF_NRQL_VARS)"
    fi
    P_IDS+=("$1"); P_RUNS+=("$2"); P_NRQLS+=("$out")
}
for ((i = 0; i < ${#Q_IDS[@]}; i++)); do
    if [[ "${Q_WHENS[$i]}" != "-" ]] && ! get_var "${Q_WHENS[$i]}" >/dev/null; then
        log "skipping ${Q_IDS[$i]}: ${Q_WHENS[$i]} is not set"; continue
    fi
    [[ "${Q_SCOPES[$i]}" == session ]] || continue
    plan "${Q_IDS[$i]}" session "${Q_NRQLS[$i]}" ""
done
for ((w = 0; w < ${#W_RUN_IDS[@]}; w++)); do
    for ((i = 0; i < ${#Q_IDS[@]}; i++)); do
        [[ "${Q_SCOPES[$i]}" == window ]] || continue
        if [[ "${Q_WHENS[$i]}" != "-" ]] && ! get_var "${Q_WHENS[$i]}" >/dev/null; then continue; fi
        if [[ "${Q_SCENS[$i]}" != all && " ${Q_SCENS[$i]} " != *" ${W_SCENS[$w]} "* ]]; then continue; fi
        plan "${Q_IDS[$i]}" "${W_RUN_IDS[$w]}" "${Q_NRQLS[$i]}" "${W_CLAUSES[$w]}"
    done
done

if [[ $opt_print -eq 1 ]]; then
    for ((i = 0; i < ${#P_IDS[@]}; i++)); do
        printf '%s\t%s\t%s\n' "${P_IDS[$i]}" "${P_RUNS[$i]}" "${P_NRQLS[$i]}"
    done
    exit 0
fi

require_account
read_key
mkdir -p "$opt_out"
failures=0
for ((i = 0; i < ${#P_IDS[@]}; i++)); do
    dest="$opt_out/${P_IDS[$i]}-${P_RUNS[$i]}.json"
    nrql_body "${P_NRQLS[$i]}" "$TMP_DIR/body.json"
    rm -f "$TMP_DIR/resp.json"
    if nerdgraph "$TMP_DIR/body.json" "$TMP_DIR/resp.json"; then
        mv "$TMP_DIR/resp.json" "$dest"
        rm -f "$dest.err"
    else
        failures=$((failures + 1))
        log "${P_IDS[$i]} for ${P_RUNS[$i]} failed; response kept in $(basename "$dest").err"
        if [[ -s "$TMP_DIR/resp.json" ]]; then mv "$TMP_DIR/resp.json" "$dest.err"; else : >"$dest.err"; fi
    fi
done
log "${#P_IDS[@]} request(s), $failures failed; results in $opt_out"
[[ $failures -eq 0 ]] || exit 1
