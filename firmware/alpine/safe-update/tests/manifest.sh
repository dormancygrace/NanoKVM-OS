#!/bin/sh
# Hash the whole root (one filesystem): path, type, mode, owner, size, sha256 or link target.
# Usage: manifest.sh > FILE. Compare two with diff to prove a rollback is byte-exact.
cd / && find . -xdev \( -path ./proc -o -path ./sys -o -path ./dev -o -path ./run -o -path ./tmp \
	-o -path ./data -o -path ./boot -o -path ./var/cache/apk \) -prune -o -print |
	sort | while IFS= read -r p; do
		if [ -L "$p" ]; then echo "$p l $(readlink "$p")"
		elif [ -f "$p" ]; then echo "$p f $(stat -c '%a %u:%g %s' "$p") $(sha256sum < "$p" | cut -c1-64)"
		else echo "$p $(stat -c '%F %a %u:%g' "$p" | tr ' ' _)"
		fi
	done
