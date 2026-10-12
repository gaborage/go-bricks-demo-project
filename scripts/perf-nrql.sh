#!/usr/bin/env bash
# scripts/perf-nrql.sh
#
# New Relic NerdGraph client for the perf harness. Three modes:
#
#   --graphql QUERY          one raw GraphQL request; response JSON on stdout
#   --query NRQL             one NRQL query against --account; response on stdout
#   --queries YAML --windows TSV --vars "K=V,..." --out DIR [--trim 1m]
#                            every query in YAML (loadtests/nrql/queries.yaml) for
#                            the windows of a windows.tsv; each raw response goes
#                            to <out>/<id>-<run_id>.json, session-scope queries
#                            to <out>/<id>-session.json. Queries that share a
#                            `group:` are sent as ONE request with each query
#                            aliased by its id under account, and saved as
#                            <out>/<group>-<run_id>.json.
#   add --print to the batch mode to print "<id> TAB <run_id> TAB <nrql>" lines
#   (grouped queries as <group>/<id>) instead of calling New Relic (no key
#   needed): the rendered text is the whole contract, so it is golden-tested and
#   diffable across labels.
#
# Which windows.tsv rows are captured: the LAST row per run_id (a re-run or a
# re-recorded idle window supersedes earlier rows, which are named in a
# warning); of those, rows with valid=1, plus the staircase scenario S5 whether
# valid or void, because its last attempt is the result either way. A run_id
# outside [A-Za-z0-9_.-] (or containing "..") is refused, since it becomes part
# of a file name. A row whose timestamps do not convert is skipped with a warning.
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
# GraphQL `errors` member; batch mode carries on, writes the response to
# <file>.err and moves an earlier <file> for the same id and run into
# <out>/attempts/<id>-<run_id>-<UTC stamp>[-N].json, the -N added when that name
# is taken, so a result and an error never coexist; a success removes a
# stale <file>.err), 2 usage (including an unresolved placeholder, caught before
# any request is sent).

case "$-" in
    *x*) echo "perf-nrql: refusing to run with xtrace on: a trace would print the New Relic key" >&2; exit 2 ;;
    *) ;;   # xtrace off: carry on
esac
case ":${SHELLOPTS:-}:" in
    *:xtrace:*) echo "perf-nrql: refusing to run with xtrace in SHELLOPTS" >&2; exit 2 ;;
    *) ;;   # xtrace not inherited: carry on
esac

set -euo pipefail
shopt -u patsub_replacement 2>/dev/null || true   # bash 5.2+: '&' in a replacement stays literal

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=lib/perf-common.sh
source "$SCRIPT_DIR/lib/perf-common.sh"

log() {
    echo "perf-nrql: $*" >&2
    return 0
}
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
  The last windows.tsv row per run_id is queried when valid=1, or for S5
  (the staircase) whether valid or void.
EOF
    return 0
}

opt_graphql="" opt_query="" opt_queries="" opt_windows="" opt_vars="" opt_out="" opt_trim="1m"
opt_account="${NEW_RELIC_ACCOUNT_ID:-}" opt_region="US" opt_key_stdin=0 opt_print=0 opt_list=0

need_value() { # FLAG VALUE
    local flag="${1-}" value="${2-}"
    [[ -n "$value" ]] || usage_err "$flag needs a value"
    return 0
}
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
    return 0
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
    return 0
}

NRQL_FIELDS='results metadata { eventTypes facets messages timeWindow { begin end } }'
# shellcheck disable=SC2016 # GraphQL variables, not shell
NRQL_GRAPHQL='query($accountId: Int!, $nrql: Nrql!) { actor { account(id: $accountId) { nrql(query: $nrql, timeout: 120) { '"$NRQL_FIELDS"' } } } }'

# group_body BODY_FILE ALIAS=NRQL... -> one request, each NRQL aliased under account
group_body() {
    local body="$1" gql vars="" sel="" k=0 pair alias nrqls=()
    shift
    for pair in "$@"; do
        alias="${pair%%=*}"
        vars="$vars, \$q$k: Nrql!"
        sel="$sel $alias: nrql(query: \$q$k, timeout: 120) { $NRQL_FIELDS }"
        nrqls+=("${pair#*=}")
        k=$((k + 1))
    done
    gql="query(\$accountId: Int!$vars) { actor { account(id: \$accountId) {$sel } } }"
    printf '%s\0' "${nrqls[@]}" | jq -Rsc --argjson acct "$opt_account" --arg gql "$gql" '
        { query: $gql,
          variables: ({ accountId: $acct } + (split("\u0000")[:-1] | to_entries | map({ ("q\(.key)"): .value }) | add)) }' >"$body"
    return 0
}

nrql_body() { # NRQL BODY_FILE
    local nrql="$1" body="$2"
    jq -cn --argjson acct "$opt_account" --arg q "$nrql" --arg gql "$NRQL_GRAPHQL" \
        '{ query: $gql, variables: { accountId: $acct, nrql: $q } }' >"$body"
    return 0
}

archive_stamp() { # UTC stamp for archive names; the offline suite pins it
    if [[ -n "${PERF_NRQL_STAMP:-}" ]]; then echo "$PERF_NRQL_STAMP"; else date -u +%Y%m%dT%H%M%SZ; fi
    return 0
}

require_account() {
    [[ -n "$opt_account" ]] || usage_err "--account or NEW_RELIC_ACCOUNT_ID is required"
    [[ "$opt_account" =~ ^[0-9]+$ ]] || usage_err "the account id must be numeric"
    return 0
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
# "Q <tab> id <tab> scope <tab> scenarios <tab> when <tab> group <tab> nrql" lines.
parse_queries() { # YAML
    local file="$1"
    if awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function unq(s) { s = trim(s); if (s ~ /^".*"$/) s = substr(s, 2, length(s) - 2); return s }
    function bad(msg) { printf "queries.yaml line %d: %s\n", NR, msg > "/dev/stderr"; failed = 1; exit 1 }
    function flush() {
        if (id != "") {
            if (nrql == "") bad("query " id " has no nrql")
            printf "Q\t%s\t%s\t%s\t%s\t%s\t%s\n", id, scope, scen, when, group, nrql
        }
        id = ""; scope = "window"; scen = "all"; when = "-"; group = "-"; nrql = ""
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
            else if (k == "group") group = unq(v)
            else if (k == "nrql") nrql = unq(v)
            else if (k != "purpose" && k != "notes") bad("unknown key " k)
            next
        }
        bad("unexpected line")
    }
    END { if (!failed) flush() }
    ' "$file"; then
        return 0
    fi
    return 1
}

parsed="$(parse_queries "$opt_queries")" || usage_err "could not parse $opt_queries"
if [[ $opt_list -eq 1 ]]; then printf '%s\n' "$parsed"; exit 0; fi
DERIVED_S="" DERIVED_R=""
Q_IDS=() Q_SCOPES=() Q_SCENS=() Q_WHENS=() Q_GROUPS=() Q_NRQLS=()
while IFS=$'\t' read -r kind a b c d e f; do
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
            [[ "$e" == "-" || "$e" =~ ^Q[0-9]+[a-z]?$ ]] || usage_err "$a: bad group $e"
            [[ "$e" == "-" || "$b" == window ]] || usage_err "$a: only window-scope queries can be grouped"
            Q_IDS+=("$a"); Q_SCOPES+=("$b"); Q_SCENS+=("$c"); Q_WHENS+=("$d"); Q_GROUPS+=("$e"); Q_NRQLS+=("$f") ;;
        *) ;;   # parse_queries emits only D and Q lines
    esac
done <<<"$parsed"
[[ -n "$DERIVED_S" && -n "$DERIVED_R" ]] || usage_err "$opt_queries must define derived S and R"
[[ ${#Q_IDS[@]} -gt 0 ]] || usage_err "no queries in $opt_queries"

# Placeholder values: PERF_NRQL_VARS, then --vars (a later key wins).
V_NAMES=() V_VALUES=()
set_var() { # NAME VALUE
    local name="$1" value="$2" i
    for ((i = 0; i < ${#V_NAMES[@]}; i++)); do
        if [[ "${V_NAMES[$i]}" == "$name" ]]; then V_VALUES[i]="$value"; return 0; fi
    done
    V_NAMES+=("$name"); V_VALUES+=("$value")
    return 0
}
get_var() { # NAME -> value; returns 1 when unset
    local name="$1" i
    for ((i = 0; i < ${#V_NAMES[@]}; i++)); do
        if [[ "${V_NAMES[$i]}" == "$name" ]]; then printf '%s' "${V_VALUES[$i]}"; return 0; fi
    done
    return 1
}
load_vars() { # "K=V,K=V" SOURCE
    local list="$1" source="$2" pair k v
    [[ -n "$list" ]] || return 0
    while IFS= read -r pair; do
        [[ -n "$pair" ]] || continue
        [[ "$pair" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || usage_err "$source: expected K=V, got '$pair'"
        k="${BASH_REMATCH[1]}"; v="${BASH_REMATCH[2]}"
        case "$k" in
            R|W) usage_err "$source: {$k} is derived and cannot be set" ;;
            S) k=SERVICE ;;   # alias: S=<service name>
            *) ;;             # every other name is taken as given
        esac
        case "$k" in
            CNS|CCT|CPOD|CNODE)
                [[ "$v" =~ ^[A-Za-z0-9_.]+$ || "$v" =~ ^\`[A-Za-z0-9_.:/-]+\`$ ]] \
                    || usage_err "$source: $k must be an attribute name (letters, digits, _ and ., or backtick-quoted)" ;;
            ROUTE_PREFIX)
                [[ "$v" =~ ^[A-Za-z0-9_./-]*$ ]] || usage_err "$source: ROUTE_PREFIX has unexpected characters" ;;
            *)
                [[ "$v" =~ ^[A-Za-z0-9_./:@+-]+$ ]] || usage_err "$source: $k must be non-empty [A-Za-z0-9_./:@+-] (no quotes or braces)" ;;
        esac
        set_var "$k" "$v"
    done < <(printf '%s\n' "$list" | tr ',' '\n')
    return 0
}
load_vars "${PERF_NRQL_VARS:-}" PERF_NRQL_VARS
load_vars "$opt_vars" --vars

# render TEMPLATE WINDOW -> the substituted NRQL; returns 1 (names printed) on leftovers
render() {
    local q="$1" window="$2" i rest name missing=""
    q="${q//\{S\}/$DERIVED_S}"
    q="${q//\{R\}/$DERIVED_R}"
    q="${q//\{W\}/$window}"
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
    return 0
}

# Windows. Empty TSV fields become "-" first: a tab IFS would collapse them.
R_LINES=() R_IDS=() R_SCENS=() R_STARTS=() R_ENDS=() R_VALIDS=() R_REASONS=()
line_no=0
while IFS=$'\t' read -r run_id scenario rep warm_start run_start run_end rc valid reason; do
    line_no=$((line_no + 1))
    [[ "$run_id" != run_id && "$run_id" != "-" ]] || continue
    : "$rep" "$warm_start" "$rc"
    # run_id becomes part of a file name.
    [[ "$run_id" =~ ^[A-Za-z0-9_.-]+$ && "$run_id" != *..* ]] \
        || usage_err "$opt_windows line $line_no: run_id '$run_id' is not [A-Za-z0-9_.-] without '..'"
    R_LINES+=("$line_no"); R_IDS+=("$run_id"); R_SCENS+=("$scenario"); R_STARTS+=("$run_start")
    R_ENDS+=("$run_end"); R_VALIDS+=("$valid"); R_REASONS+=("$reason")
done < <(awk -F'\t' 'BEGIN { OFS = "\t" } NF > 0 { for (i = 1; i <= 9; i++) if ($i == "") $i = "-"; print $1, $2, $3, $4, $5, $6, $7, $8, $9 }' "$opt_windows")

# The last row per run_id is authoritative: a re-run or a re-recorded idle
# window supersedes the earlier rows.
W_RUN_IDS=() W_SCENS=() W_CLAUSES=()
for ((r = 0; r < ${#R_IDS[@]}; r++)); do
    later=""
    for ((s = r + 1; s < ${#R_IDS[@]}; s++)); do
        [[ "${R_IDS[$s]}" != "${R_IDS[$r]}" ]] || later="${R_LINES[$s]}"
    done
    if [[ -n "$later" ]]; then
        log "warning: ${R_IDS[$r]}: windows.tsv line ${R_LINES[$r]} is superseded by line $later; capturing the last row only"
        continue
    fi
    run_id="${R_IDS[$r]}" scenario="${R_SCENS[$r]}"
    if [[ "${R_VALIDS[$r]}" != 1 ]]; then
        if [[ "$scenario" != S5 ]]; then log "skipping $run_id: not valid (${R_REASONS[$r]})"; continue; fi
        log "notice: $run_id is void (${R_REASONS[$r]}); captured anyway: a staircase's last attempt is its result"
    fi
    if ! is_iso "${R_STARTS[$r]}" || ! is_iso "${R_ENDS[$r]}"; then log "skipping $run_id: no run window"; continue; fi
    start_s="$(iso_to_epoch "${R_STARTS[$r]}")"; end_s="$(iso_to_epoch "${R_ENDS[$r]}")"
    if [[ -z "$start_s" || -z "$end_s" ]]; then
        log "warning: skipping $run_id: run_start '${R_STARTS[$r]}' or run_end '${R_ENDS[$r]}' is not a valid time"
        continue
    fi
    since_s=$(((start_s + trim_s) / 60 * 60)); until_s=$(((end_s - trim_s) / 60 * 60))
    if ((until_s <= since_s)); then log "skipping $run_id: the window is empty after a ${trim_s}s trim"; continue; fi
    W_RUN_IDS+=("$run_id"); W_SCENS+=("$scenario")
    W_CLAUSES+=("SINCE $((since_s * 1000)) UNTIL $((until_s * 1000))")
done

# Plan every request first, so an unresolved placeholder stops the run before
# anything is sent. A request has one part, or one part per grouped query.
REQ_FILES=() REQ_RUNS=() REQ_GROUPED=() PART_REQS=() PART_IDS=() PART_NRQLS=()
plan() { # FILE_ID QUERY_ID GROUPED RUN_ID TEMPLATE WINDOW FIRST_REQ
    local file="$1" qid="$2" grouped="$3" run="$4" template="$5" window="$6" first="$7" out req="" k
    if ! out="$(render "$template" "$window")"; then
        usage_err "$qid: unresolved placeholder(s): $out (set them in --vars or PERF_NRQL_VARS)"
    fi
    if [[ "$grouped" -eq 1 ]]; then
        for ((k = first; k < ${#REQ_FILES[@]}; k++)); do
            if [[ "${REQ_FILES[$k]}" == "$file" && "${REQ_RUNS[$k]}" == "$run" ]]; then req="$k"; fi
        done
    fi
    if [[ -z "$req" ]]; then
        req="${#REQ_FILES[@]}"
        REQ_FILES+=("$file"); REQ_RUNS+=("$run"); REQ_GROUPED+=("$grouped")
    fi
    PART_REQS+=("$req"); PART_IDS+=("$qid"); PART_NRQLS+=("$out")
    return 0
}
for ((i = 0; i < ${#Q_IDS[@]}; i++)); do
    if [[ "${Q_WHENS[$i]}" != "-" ]] && ! get_var "${Q_WHENS[$i]}" >/dev/null; then
        log "skipping ${Q_IDS[$i]}: ${Q_WHENS[$i]} is not set"; continue
    fi
    [[ "${Q_SCOPES[$i]}" == session ]] || continue
    plan "${Q_IDS[$i]}" "${Q_IDS[$i]}" 0 session "${Q_NRQLS[$i]}" "" 0
done
for ((w = 0; w < ${#W_RUN_IDS[@]}; w++)); do
    first="${#REQ_FILES[@]}"
    for ((i = 0; i < ${#Q_IDS[@]}; i++)); do
        [[ "${Q_SCOPES[$i]}" == window ]] || continue
        if [[ "${Q_WHENS[$i]}" != "-" ]] && ! get_var "${Q_WHENS[$i]}" >/dev/null; then continue; fi
        if [[ "${Q_SCENS[$i]}" != all && " ${Q_SCENS[$i]} " != *" ${W_SCENS[$w]} "* ]]; then continue; fi
        if [[ "${Q_GROUPS[$i]}" == "-" ]]; then
            plan "${Q_IDS[$i]}" "${Q_IDS[$i]}" 0 "${W_RUN_IDS[$w]}" "${Q_NRQLS[$i]}" "${W_CLAUSES[$w]}" "$first"
        else
            plan "${Q_GROUPS[$i]}" "${Q_IDS[$i]}" 1 "${W_RUN_IDS[$w]}" "${Q_NRQLS[$i]}" "${W_CLAUSES[$w]}" "$first"
        fi
    done
done

if [[ $opt_print -eq 1 ]]; then
    for ((p = 0; p < ${#PART_IDS[@]}; p++)); do
        req="${PART_REQS[$p]}" label="${PART_IDS[$p]}"
        [[ "${REQ_GROUPED[$req]}" -eq 0 ]] || label="${REQ_FILES[$req]}/${PART_IDS[$p]}"
        printf '%s\t%s\t%s\n' "$label" "${REQ_RUNS[$req]}" "${PART_NRQLS[$p]}"
    done
    exit 0
fi

require_account
read_key
mkdir -p "$opt_out"
failures=0
for ((r = 0; r < ${#REQ_FILES[@]}; r++)); do
    dest="$opt_out/${REQ_FILES[$r]}-${REQ_RUNS[$r]}.json"
    if [[ "${REQ_GROUPED[$r]}" -eq 1 ]]; then
        parts=()
        for ((p = 0; p < ${#PART_IDS[@]}; p++)); do
            [[ "${PART_REQS[$p]}" -ne "$r" ]] || parts+=("${PART_IDS[$p]}=${PART_NRQLS[$p]}")
        done
        group_body "$TMP_DIR/body.json" "${parts[@]}"
    else
        for ((p = 0; p < ${#PART_IDS[@]}; p++)); do
            [[ "${PART_REQS[$p]}" -ne "$r" ]] || nrql_body "${PART_NRQLS[$p]}" "$TMP_DIR/body.json"
        done
    fi
    rm -f "$TMP_DIR/resp.json"
    # A result and an error for the same <id>-<run_id> never coexist: a reader must
    # not take an earlier capture's .json for this attempt's outcome.
    if nerdgraph "$TMP_DIR/body.json" "$TMP_DIR/resp.json"; then
        mv "$TMP_DIR/resp.json" "$dest"
        rm -f "$dest.err"
    else
        failures=$((failures + 1))
        if [[ -e "$dest" ]]; then
            mkdir -p "$opt_out/attempts"
            stale="$(free_path "$opt_out/attempts/$(basename "$dest" .json)-$(archive_stamp)" json)"
            mv "$dest" "$stale"
            log "moved the earlier $(basename "$dest") to attempts/ ($(basename "$stale"))"
        fi
        log "${REQ_FILES[$r]} for ${REQ_RUNS[$r]} failed; response kept in $(basename "$dest").err"
        if [[ -s "$TMP_DIR/resp.json" ]]; then mv "$TMP_DIR/resp.json" "$dest.err"; else : >"$dest.err"; fi
    fi
done
log "${#REQ_FILES[@]} request(s), $failures failed; results in $opt_out"
[[ $failures -eq 0 ]] || exit 1
