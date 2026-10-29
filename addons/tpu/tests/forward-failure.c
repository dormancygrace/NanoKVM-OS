// Fault injection for nanokvm-ocr on a device: preloaded, it makes the Nth
// CVI_NN_Forward (NANOKVM_OCR_FAIL_FORWARD=N) fail, as a TPU fault would.
// forward-failure.sh runs the helper with it; see the README of addons/tpu.
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdlib.h>

typedef int (*forward_fn)(void *, void *, int, void *, int);

int CVI_NN_Forward(void *model, void *inputs, int input_num, void *outputs, int output_num)
{
	static int calls;
	static forward_fn real;
	const char *fail = getenv("NANOKVM_OCR_FAIL_FORWARD");
	if (fail && ++calls == atoi(fail))
		return -1;
	if (!real)
		real = (forward_fn)dlsym(RTLD_NEXT, "CVI_NN_Forward");
	return real(model, inputs, input_num, outputs, output_num);
}
