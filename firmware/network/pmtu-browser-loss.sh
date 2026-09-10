#!/bin/sh
# Bounded IPv4 browser PMTU fault injection. Requires nft and root.
# Only oversized UDP to the supplied peer is dropped. No ICMP is generated.
# Refuses an existing test table and cleans only its own table on exit.
set -eu
name=nkos_pmtu_test
peer=${1:?Pass the browser IPv4 address}
log=${2:-/tmp/pmtu-browser-loss.txt}
cleanup() { nft delete table ip "$name" 2>/dev/null || true; echo CLEANED >> "$log"; }
nft list table ip "$name" >/dev/null 2>&1 && exit 2
nft add table ip "$name"
trap cleanup EXIT INT TERM HUP
nft add chain ip "$name" output '{ type filter hook output priority -10; policy accept; }'
nft add rule ip "$name" output ip daddr "$peer" meta l4proto udp meta length '>' 1380 counter drop
echo START_IP_LIMIT_1380 > "$log"
for step in $(seq 1 75); do
 if [ "$step" -eq 36 ]; then
  nft list table ip "$name" >> "$log"
  nft flush chain ip "$name" output
  nft add rule ip "$name" output ip daddr "$peer" meta l4proto udp meta length '>' 1260 counter drop
  echo LOWER_IP_LIMIT_1260 >> "$log"
 fi
 printf 'T %s FPS ' "$step" >> "$log"
 cat /run/nanokvm/now_fps >> "$log"
 printf '\n' >> "$log"
 nft list chain ip "$name" output | grep "counter packets" >> "$log"
 sleep 1
done
nft list table ip "$name" >> "$log"
grep 'path budget' /run/nanokvm/pmtu.log | tail -n 12 >> "$log"
