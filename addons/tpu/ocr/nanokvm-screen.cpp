// SPDX-License-Identifier: GPL-3.0-or-later
// nanokvm-screen: what the host's screen shows, by a small classifier on the
// CV181x TPU (addons/tpu/models/screen): a desktop or an application, a
// Windows stop screen, a text console (where a kernel panic shows), a blank
// screen, or a firmware or installer screen.
//
//   nanokvm-screen SCREEN.cvimodel < picture
//
// stdin: "P WIDTH HEIGHT\n", then the B, G and R planes at the model's input
// size (VPSS scales the capture to it).
// stdout: {"class":"bsod","score":0.97,"scores":{"normal":0.01,...},"ms":3}
// Errors go to stderr, exit status 1.
#include "cviruntime.h"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <vector>

namespace {

// The model's outputs, in the order of synth.CLASSES.
const char *const classes[] = {"normal", "bsod", "console", "blank", "firmware"};
constexpr int class_count = sizeof(classes) / sizeof(classes[0]);

float get(CVI_TENSOR *t, size_t i) {
    const void *p = CVI_NN_TensorPtr(t);
    switch (t->fmt) {
    case CVI_FMT_FP32: return static_cast<const float *>(p)[i];
    case CVI_FMT_BF16: {
        const uint32_t u = static_cast<uint32_t>(static_cast<const uint16_t *>(p)[i]) << 16;
        float v;
        std::memcpy(&v, &u, 4);
        return v;
    }
    case CVI_FMT_INT8: return static_cast<const int8_t *>(p)[i] / CVI_NN_TensorQuantScale(t);
    default: return NAN;
    }
}

int fail(const char *message) {
    std::fprintf(stderr, "nanokvm-screen: %s\n", message);
    return 1;
}

} // namespace

int main(int argc, char **argv) {
    if (argc != 2) return fail("usage: nanokvm-screen SCREEN.cvimodel < picture");
    const auto start = std::chrono::steady_clock::now();
    int width = 0, height = 0;
    if (std::scanf("P %d %d", &width, &height) != 2 || std::getchar() != '\n' || width < 16 || height < 16
        || width > 1024 || height > 1024)
        return fail("invalid picture header");
    std::vector<uint8_t> planes(static_cast<size_t>(width) * height * 3);
    if (std::fread(planes.data(), 1, planes.size(), stdin) != planes.size()) return fail("short picture");

    CVI_MODEL_HANDLE model = nullptr;
    if (CVI_NN_RegisterModel(argv[1], &model) != 0) return fail("cannot load the model");
    CVI_TENSOR *inputs, *outputs;
    int32_t input_count, output_count;
    if (CVI_NN_GetInputOutputTensors(model, &inputs, &input_count, &outputs, &output_count) != 0
        || input_count != 1 || output_count != 1) {
        CVI_NN_CleanupModel(model);
        return fail("unexpected model tensors");
    }
    const CVI_SHAPE in = CVI_NN_TensorShape(&inputs[0]);
    const CVI_SHAPE out = CVI_NN_TensorShape(&outputs[0]);
    size_t out_count = 1;
    for (size_t i = 0; i < out.dim_size; ++i) out_count *= out.dim[i];
    // The scaling to 0..1 is fused into the model: it takes the 8-bit planes.
    if (in.dim_size != 4 || in.dim[1] != 3 || in.dim[2] != height || in.dim[3] != width
        || inputs[0].fmt != CVI_FMT_UINT8 || out_count != class_count) {
        CVI_NN_CleanupModel(model);
        return fail("the picture or the model is not the classifier's size");
    }
    std::memcpy(CVI_NN_TensorPtr(&inputs[0]), planes.data(), planes.size());
    if (CVI_NN_Forward(model, inputs, input_count, outputs, output_count) != 0) {
        CVI_NN_CleanupModel(model);
        return fail("the TPU failed");
    }
    float scores[class_count];
    float top = -INFINITY, sum = 0;
    for (int i = 0; i < class_count; ++i) top = std::max(top, scores[i] = get(&outputs[0], i));
    for (float &s : scores) sum += s = std::exp(s - top);
    int best = 0;
    for (int i = 0; i < class_count; ++i) {
        scores[i] /= sum;
        if (scores[i] > scores[best]) best = i;
    }
    CVI_NN_CleanupModel(model);
    const long ms = std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - start).count();
    std::printf("{\"class\":\"%s\",\"score\":%.4f,\"scores\":{", classes[best], scores[best]);
    for (int i = 0; i < class_count; ++i) std::printf("%s\"%s\":%.4f", i ? "," : "", classes[i], scores[i]);
    std::printf("},\"ms\":%ld}\n", ms);
    return 0;
}
