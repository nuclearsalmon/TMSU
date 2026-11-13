#!/usr/bin/env bash
set -eu

suite=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=$(mktemp -d /tmp/tmsu-runner-check.XXXXXXXX)
trap 'rm -rf -- "$workspace"' EXIT
mkdir -p "$workspace/bin"
cat >"$workspace/bin/tmsu" <<'EOF'
#!/usr/bin/env bash
[[ $1 == init && -d $2 && -z ${TMSU_DB:-} ]] || exit 1
if [[ ${FAIL_INIT:-0} == 1 ]]; then
    printf '%s\n' "$2" >> "$RECORD"
    exit 1
fi
mkdir -p "$2/.tmsu"
EOF
cat >"$workspace/pass" <<'EOF'
#!/usr/bin/env bash
[[ -d $TEST_DIR && $HOME == "$TEST_DIR/home" && $TMSU_DB == "$TEST_DIR/.tmsu/db" ]] || exit 1
printf '%s\n' "$TEST_DIR" >> "$RECORD"
EOF
cat >"$workspace/fail" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$TEST_DIR" >> "$RECORD"
exit 1
EOF
chmod +x "$workspace/bin/tmsu" "$workspace/pass" "$workspace/fail"
export TMSU_BIN_DIR="$workspace/bin" RECORD="$workspace/dirs" TMSU_DB=/should/not/be/used
"$suite/run" "$workspace/pass" "$workspace/pass"
if "$suite/run" "$workspace/fail"; then
    echo 'runner accepted a failing test' >&2
    exit 1
fi
if FAIL_INIT=1 "$suite/run" "$workspace/pass"; then
    echo 'runner accepted a failed setup' >&2
    exit 1
fi
mapfile -t dirs < "$RECORD"
[[ ${#dirs[@]} -eq 4 ]]
[[ $(printf '%s\n' "${dirs[@]}" | sort -u | wc -l) -eq 4 ]]
for dir in "${dirs[@]}"; do
    [[ ! -e $dir ]] || { echo "test directory leaked: $dir" >&2; exit 1; }
done
if "$suite/teardown" "$workspace" 2>/dev/null; then
    echo 'teardown accepted an unsafe path' >&2
    exit 1
fi
echo 'runner regression checks passed'
