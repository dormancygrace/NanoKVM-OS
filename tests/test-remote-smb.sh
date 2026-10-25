#!/usr/bin/env bash
# A disposable real SMB2/3 peer for the production source and service tests.
# Docker and Go are required. Nothing is mounted through the host's CIFS driver.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
scratch=$(mktemp -d)
container="nanokvm-smb-test-$$"
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1 || true
    rm -rf -- "$scratch"
}
trap cleanup EXIT
mkdir "$scratch/share"
python3 - "$scratch/share/test.iso" <<'PY'
import sys
block = bytes((i * 37 + i // 1009) % 251 for i in range(1048576))
with open(sys.argv[1], 'wb') as image:
    for _ in range(64):
        image.write(block)
PY
docker run -d --name "$container" -p 127.0.0.1::445 \
    -v "$scratch/share:/share:ro" \
    -v "$root/tests/fixtures/remote-smb/smb.conf:/etc/samba/smb.conf:ro" \
    alpine:3.24 sh -c '
        apk add --no-cache samba-server samba-common-tools >/tmp/setup.log 2>&1 &&
        adduser -D -H media &&
        printf "nano-fixture\nnano-fixture\n" | smbpasswd -s -a media &&
        exec smbd --foreground --no-process-group --debug-stdout < /dev/null
    ' >/dev/null
address=$(docker port "$container" 445/tcp)
ready=false
for ((i=0; i<60; i++)); do
    if docker exec "$container" test -f /run/samba/smbd.pid; then
        ready=true
        break
    fi
    sleep 1
done
if [[ "$ready" != true ]]; then
    docker logs "$container"
    docker exec "$container" cat /tmp/setup.log || true
    exit 1
fi
cd "$root/server"
NANOKVM_SMB_TEST_ADDR="$address" go test -tags teststub,nomsgpack -race -count=1 ./remotemedia ./service/storage
