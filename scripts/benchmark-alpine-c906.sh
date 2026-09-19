#!/bin/sh
# Small, reproducible stock-vs-C906 comparison for the physical NanoKVM.
set -eu

WARMUPS=${WARMUPS:-1}
ITERATIONS=${ITERATIONS:-10}
XZ_ITERATIONS=${XZ_ITERATIONS:-3}
PROFILE=${PROFILE:-$(cat /etc/nanokvm-build-profile 2>/dev/null || echo unknown)}
CORPUS_DIR=${CORPUS_DIR:-/data/nanokvm-bench}
OUTPUT=${OUTPUT:-$CORPUS_DIR/results-$PROFILE-$(date -u +%Y%m%dT%H%M%SZ)}
TIME=${TIME:-/usr/bin/time}

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -x "$TIME" ] || { echo "install Alpine package 'time'" >&2; exit 1; }
mkdir -p "$CORPUS_DIR"
[ ! -e "$OUTPUT" ] || { echo "output already exists: $OUTPUT" >&2; exit 1; }
mkdir "$OUTPUT"

ensure_corpus() {
    file=$1
    source=$2
    size=$(wc -c < "$file" 2>/dev/null || echo 0)
    if [ "$size" -ne 67108864 ]; then
        rm -f "$file"
        dd if="$source" of="$file" bs=1M count=64 conv=fsync
    fi
}
ensure_corpus "$CORPUS_DIR/zero-64m.bin" /dev/zero
ensure_corpus "$CORPUS_DIR/random-64m.bin" /dev/urandom

{
    echo "profile=$PROFILE"
    echo "warmups=$WARMUPS"
    echo "iterations=$ITERATIONS"
    echo "xz_iterations=$XZ_ITERATIONS"
    echo "date=$(date -u +%FT%TZ)"
    echo "kernel=$(uname -srvmo)"
    echo "alpine=$(cat /etc/alpine-release)"
    fit_path=${FIT_PATH:-/boot/boot-alpine-test.sd}
    [ -f "$fit_path" ] || fit_path=/boot/boot.sd
    [ -f "$fit_path" ] || { echo "missing FIT: $fit_path" >&2; exit 1; }
    echo "fit_path=$fit_path"
    echo "fit_sha256=$(sha256sum "$fit_path" | awk '{print $1}')"
    echo "cpu_governor=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo unavailable)"
    echo "cpu_frequency_khz=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq 2>/dev/null || echo unavailable)"
    echo "temperature_millic=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo unavailable)"
    echo "packages_sha256=$(apk info -v | sort | sha256sum | awk '{print $1}')"
    echo "meminfo:"
    sed -n '/^MemTotal:/p;/^MemAvailable:/p;/^SwapTotal:/p' /proc/meminfo
} > "$OUTPUT/metadata.txt"
apk info -v | sort > "$OUTPUT/packages.txt"
cp /etc/apk/repositories "$OUTPUT/repositories.txt"
cp /etc/apk/world "$OUTPUT/world.txt"
cat /proc/cpuinfo > "$OUTPUT/cpuinfo.txt"
for zone in /sys/class/thermal/thermal_zone*; do
    [ -d "$zone" ] || continue
    printf '%s\t' "$(basename "$zone")"
    cat "$zone/type" 2>/dev/null || echo unknown
done > "$OUTPUT/thermal-zones.txt"

printf 'workload\titeration\treal_s\tuser_s\tsystem_s\tmaxrss_kib\tfrequency_before_khz\tfrequency_after_khz\ttemperature_before_millic\ttemperature_after_millic\n' > "$OUTPUT/timings.tsv"

run_one() {
    workload=$1
    shift
    warmup=1
    while [ "$warmup" -le "$WARMUPS" ]; do
        sync
        printf '3\n' > /proc/sys/vm/drop_caches
        "$@" >/dev/null
        warmup=$((warmup + 1))
    done
    iteration=1
    while [ "$iteration" -le "$ITERATIONS" ]; do
        sync
        printf '3\n' > /proc/sys/vm/drop_caches
        frequency_before=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq 2>/dev/null || echo unavailable)
        temperature_before=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo unavailable)
        "$TIME" -f '%e\t%U\t%S\t%M' -o "$OUTPUT/.time" "$@" >/dev/null
        values=$(cat "$OUTPUT/.time")
        frequency_after=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq 2>/dev/null || echo unavailable)
        temperature_after=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo unavailable)
        printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
            "$workload" "$iteration" "$values" "$frequency_before" "$frequency_after" \
            "$temperature_before" "$temperature_after" >> "$OUTPUT/timings.tsv"
        iteration=$((iteration + 1))
    done
}

run_one_count() {
    count=$1
    shift
    saved_iterations=$ITERATIONS
    ITERATIONS=$count
    run_one "$@"
    ITERATIONS=$saved_iterations
}

run_one sha256-random sha256sum "$CORPUS_DIR/random-64m.bin"
run_one gzip-zero sh -c "gzip -1 -c '$CORPUS_DIR/zero-64m.bin' > '$OUTPUT/zero.gz'"
run_one gzip-random sh -c "gzip -1 -c '$CORPUS_DIR/random-64m.bin' > '$OUTPUT/random.gz'"
run_one gzip-decode sh -c "gzip -dc '$OUTPUT/random.gz' >/dev/null"
run_one lz4-random sh -c "lz4 -q -f '$CORPUS_DIR/random-64m.bin' '$OUTPUT/random.lz4'"
run_one lz4-decode sh -c "lz4 -q -d -c '$OUTPUT/random.lz4' >/dev/null"
run_one zstd-random sh -c "zstd -q -1 -f '$CORPUS_DIR/random-64m.bin' -o '$OUTPUT/random.zst'"
run_one zstd-decode sh -c "zstd -q -d -c '$OUTPUT/random.zst' >/dev/null"
run_one_count "$XZ_ITERATIONS" xz-random sh -c "xz -1 -c '$CORPUS_DIR/random-64m.bin' > '$OUTPUT/random.xz'"
run_one_count "$XZ_ITERATIONS" xz-decode sh -c "xz -dc '$OUTPUT/random.xz' >/dev/null"
run_one apk-solver apk add --simulate openssl curl nano

openssl speed -seconds 5 -bytes 16384 sha256 aes-128-cbc \
    > "$OUTPUT/openssl-sha-aes-cbc.txt" 2>&1
openssl speed -seconds 5 -bytes 16384 -evp aes-128-gcm \
    > "$OUTPUT/openssl-aes-gcm.txt" 2>&1
openssl speed -seconds 5 -bytes 16384 -evp chacha20-poly1305 \
    > "$OUTPUT/openssl-chacha20-poly1305.txt" 2>&1
ps -eo pid,comm,rss,vsz,time,args > "$OUTPUT/processes.txt"
dmesg > "$OUTPUT/dmesg.txt"

gzip -dc "$OUTPUT/zero.gz" | cmp - "$CORPUS_DIR/zero-64m.bin"
gzip -dc "$OUTPUT/random.gz" | cmp - "$CORPUS_DIR/random-64m.bin"
lz4 -q -d -c "$OUTPUT/random.lz4" | cmp - "$CORPUS_DIR/random-64m.bin"
zstd -q -d -c "$OUTPUT/random.zst" | cmp - "$CORPUS_DIR/random-64m.bin"
xz -dc "$OUTPUT/random.xz" | cmp - "$CORPUS_DIR/random-64m.bin"
sha256sum "$CORPUS_DIR/zero-64m.bin" "$CORPUS_DIR/random-64m.bin" \
    > "$OUTPUT/corpus.sha256"
sha256sum "$OUTPUT/zero.gz" "$OUTPUT/random.gz" "$OUTPUT/random.lz4" \
    "$OUTPUT/random.zst" "$OUTPUT/random.xz" > "$OUTPUT/compressed.sha256"
wc -c "$OUTPUT/zero.gz" "$OUTPUT/random.gz" "$OUTPUT/random.lz4" \
    "$OUTPUT/random.zst" "$OUTPUT/random.xz" > "$OUTPUT/compressed-sizes.txt"
echo 'zero gzip and all random compressed outputs match their source corpus' \
    > "$OUTPUT/correctness.txt"

rm -f "$OUTPUT/.time" "$OUTPUT/zero.gz" "$OUTPUT/random.gz" \
      "$OUTPUT/random.lz4" "$OUTPUT/random.zst" "$OUTPUT/random.xz"
echo "$OUTPUT"
