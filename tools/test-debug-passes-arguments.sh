#!/bin/sh
# Exercise the debug() declaration and definition extracted from production.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
SOURCE=${1:-"$ROOT/support/sg2002/additional/kvm/src/kvm_vision.cpp"}
CXX=${CXX:-g++}
FORMAT_CXX=${FORMAT_CXX:-$CXX}

[ -f "$SOURCE" ] || { echo "missing source: $SOURCE" >&2; exit 2; }
command -v "$CXX" >/dev/null 2>&1 || { echo "missing compiler: $CXX" >&2; exit 2; }
command -v "$FORMAT_CXX" >/dev/null 2>&1 || { echo "missing format compiler: $FORMAT_CXX" >&2; exit 2; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

DECLARATION=$(sed -n '/^void debug(const char \*format, \.\.\.) __attribute__((format(printf, 1, 2)));$/p' "$SOURCE")
DEFINITION=$(sed -n '/^void debug(const char \*format, \.\.\.)$/,/^}$/p' "$SOURCE")

[ -n "$DECLARATION" ] || { echo "debug() is missing its printf format attribute" >&2; exit 1; }
[ -n "$DEFINITION" ] || { echo "could not extract debug() from $SOURCE" >&2; exit 1; }

emit_prelude() {
    printf '%s\n' '#include <cstdarg>' '#include <cstdint>' '#include <cstdio>'
    printf '%s\n' 'uint8_t debug_en = 0;'
    printf '%s\n' "$DECLARATION" "$DEFINITION"
}

{
    emit_prelude
    cat <<'EOF'
int main(int argc, char **argv)
{
    if (argc != 2) return 2;
    if (argv[1][0] == '0') {
        debug_en = 0;
        debug("disabled %d\n", 41);
        return 0;
    }
    debug_en = 1;
    debug("mixed %s %d %x; load=%d%%\n", "value", 41, 255, 90);
    return 0;
}
EOF
} > "$WORK/runtime.cpp"

"$CXX" -std=gnu++17 -O2 -Wall -Wextra -Wformat=2 -Werror \
    "$WORK/runtime.cpp" -o "$WORK/runtime"

"$WORK/runtime" 0 > "$WORK/disabled.out"
[ ! -s "$WORK/disabled.out" ] || {
    echo "debug() printed while disabled" >&2
    exit 1
}

"$WORK/runtime" 1 > "$WORK/enabled.out"
printf '%s\n' 'mixed value 41 ff; load=90%' > "$WORK/expected.out"
cmp "$WORK/expected.out" "$WORK/enabled.out"

{
    emit_prelude
    printf '%s\n' 'int main(){ debug("value=%d\n", 41); return 0; }'
} > "$WORK/correct.cpp"
"$FORMAT_CXX" -std=gnu++17 -O2 -Wall -Wextra -Wformat=2 -Werror \
    -c "$WORK/correct.cpp" -o "$WORK/correct.o"

{
    emit_prelude
    printf '%s\n' 'int main(){ debug("value=%d\n", "wrong type"); return 0; }'
} > "$WORK/wrong.cpp"
if "$FORMAT_CXX" -std=gnu++17 -O2 -Wall -Wextra -Wformat=2 -Werror \
        -c "$WORK/wrong.cpp" -o "$WORK/wrong.o" > "$WORK/wrong.log" 2>&1; then
    echo "mismatched debug() format unexpectedly compiled" >&2
    exit 1
fi
grep -Eq "format.*(expects argument|expects argument of type)|expects argument.*format" "$WORK/wrong.log" || {
    echo "mismatched call failed without the expected format diagnostic" >&2
    sed 's/^/  /' "$WORK/wrong.log" >&2
    exit 1
}

echo "PASS: production debug() is silent when disabled, formats arguments when enabled, and rejects a mismatched call"
