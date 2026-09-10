#!/bin/sh
# Bounded silent-drop test for one IPv4 UDP ICE flow. Other viewers are untouched.
set -eu
[ "$#" -eq 4 ] || { echo "usage: $0 peer-ipv4 source-port destination-port log-file" >&2; exit 2; }
peer=$1
source_port=$2
destination_port=$3
log=$4
for port in "$source_port" "$destination_port"; do
 case "$port" in ''|*[!0-9]*) exit 2;; esac
 [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || exit 2
done
name=nkos_pmtu_peer_test
nft list table ip "$name" >/dev/null 2>&1 && exit 2
cleanup() { nft list table ip "$name" >> "$log" 2>/dev/null || true; nft delete table ip "$name" 2>/dev/null || true; echo CLEANED >> "$log"; }
nft add table ip "$name"
trap cleanup EXIT INT TERM HUP
nft add chain ip "$name" output '{ type filter hook output priority -10; policy accept; }'
nft add rule ip "$name" output ip daddr "$peer" udp sport "$source_port" udp dport "$destination_port" meta length '>' 1260 counter drop
echo START_SINGLE_FLOW_IP1260 > "$log"
for step in $(seq 1 90); do
 if [ $((step % 5)) -eq 0 ]; then
  printf 'T %s FPS ' "$step" >> "$log"
  cat /run/nanokvm/now_fps >> "$log"
  printf '\n' >> "$log"
  nft list chain ip "$name" output >> "$log"
 fi
 sleep 1
done
