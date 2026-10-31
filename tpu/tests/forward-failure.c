// Fault injection for nanokvm-ocr on a device: linked into a test build of
// the helper with -Wl,--wrap=CVI_NN_Forward (the helper is static), it makes
// the Nth CVI_NN_Forward (NANOKVM_OCR_FAIL_FORWARD=N) fail, as a TPU fault
// would. forward-failure.sh runs that build; see tpu/README.md.
#include <stdlib.h>

int __real_CVI_NN_Forward(void *model, void *inputs, int input_num, void *outputs, int output_num);

int __wrap_CVI_NN_Forward(void *model, void *inputs, int input_num, void *outputs, int output_num)
{
	static int calls;
	const char *fail = getenv("NANOKVM_OCR_FAIL_FORWARD");
	if (fail && ++calls == atoi(fail))
		return -1;
	return __real_CVI_NN_Forward(model, inputs, input_num, outputs, output_num);
}
