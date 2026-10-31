#!/bin/sh
# On a NanoKVM with the Latin OCR models: a failed TPU run must fail the read
# (exit status 1, no summary line), not report a screen without text.
#   forward-failure.sh HELPER PICTURE   (PICTURE: "P W H\n" + B, G, R planes)
# HELPER is nanokvm-ocr built with forward-failure.c (tpu/README.md). The
# first run is the detector, the second the first line's recognizer.
set -u
helper=$1 picture=$2
models=/usr/lib/nanokvm-tpu/models
run() {
	NANOKVM_OCR_FAIL_FORWARD=$1 \
		"$helper" $models/ocr-det.cvimodel $models/latin/ocr-rec.cvimodel:3 \
		$models/latin/ocr-dict.txt <"$picture" >/tmp/forward-failure.out 2>/tmp/forward-failure.err
}
failed=0
run 0; status=$?
if [ $status -ne 0 ] || ! grep -q '"done":true' /tmp/forward-failure.out; then
	echo "FAIL: without a fault the read failed ($status)"; failed=1
fi
for n in 1 2; do
	what=detector; [ $n = 2 ] && what=recognizer
	run $n; status=$?
	if [ $status -eq 1 ] && ! grep -q '"done"' /tmp/forward-failure.out; then
		echo "PASS: $what fault -> exit 1: $(cat /tmp/forward-failure.err)"
	else
		echo "FAIL: $what fault -> exit $status"; failed=1
	fi
done
rm -f /tmp/forward-failure.out /tmp/forward-failure.err
exit $failed
