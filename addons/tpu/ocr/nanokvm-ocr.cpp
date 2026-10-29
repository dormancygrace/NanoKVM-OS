// SPDX-License-Identifier: GPL-3.0-or-later
// nanokvm-ocr: the text on a picture of the screen, with PaddleOCR models
// (PP-OCR text detection and recognition) on the CV181x TPU.
//
//   nanokvm-ocr DET.cvimodel REC.cvimodel DICT.txt [SURE.cvimodel] < picture
//
// Several recognition models of one height may be given, comma-separated:
// each line goes through the narrowest that holds it. REC.cvimodel:N is one
// model with N programs, its widths, sharing the weights. SURE, a bigger
// recognizer with the same dictionary (given the same way), reads again the
// lines with a doubtful character.
//
// stdin: "WIDTH HEIGHT\n", then WIDTH*HEIGHT*3 bytes of packed RGB, or
// "P WIDTH HEIGHT\n", then the B, G and R planes (WIDTH*HEIGHT bytes each),
// as VPSS produces them and as the models take them.
// stdout: one JSON object per word, then a summary:
//   {"text":"...","score":0.97,"x":10,"y":20,"w":80,"h":16}
//   {"done":true,"words":12,"reread":2,"det_ms":310,"rec_ms":240}
// Boxes are in picture pixels. Errors go to stderr, exit status 1;
// NANOKVM_OCR_DEBUG=1 also prints the models and every box there.
//
// Detection runs on the picture at half size when it is wider than 1280 (the
// detector still finds 10-20 px screen text at 5-10 px), in tiles of the
// model's input size; recognition reads the boxes from the full picture.
// The DB probability map gives text regions; a region wider than the
// recognition input is cut at its widest blank columns, so the input holds
// it without squeezing it. Recognition is CTC: the most likely character
// per step, without blanks and repeats.
#include "cviruntime.h"

#include <dirent.h>
#include <fcntl.h>
#include <linux/dma-heap.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <unistd.h>
#include <riscv_vector.h> // XTheadVector: build with -march=rv64gc_xtheadvector

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <memory>
#include <optional>
#include <string>
#include <thread>
#include <vector>

namespace {

// B, G and R planes, the models' layout; at() takes an RGB channel index.
struct Picture {
    int width = 0, height = 0;
    std::vector<uint8_t> bgr;
    const uint8_t *plane(int bgr_index) const { return &bgr[static_cast<size_t>(bgr_index) * width * height]; }
    uint8_t at(int x, int y, int c) const { return plane(2 - c)[static_cast<size_t>(y) * width + x]; }
};

// A line of text: w x h from (x, y), its top edge rising slope pixels per
// pixel to the right (a photo of a screen is seldom level).
struct Box {
    int x, y, w, h;
    float score;
    float slope = 0;
    // The upright rectangle around it.
    int top() const { return y + static_cast<int>(std::floor(std::min(0.0f, slope * w))); }
    int bottom() const { return y + h + static_cast<int>(std::ceil(std::max(0.0f, slope * w))); }
};

struct Model {
    CVI_MODEL_HANDLE handle = nullptr;
    CVI_TENSOR *inputs = nullptr, *outputs = nullptr;
    int32_t input_count = 0, output_count = 0;
    int in_h = 0, in_w = 0;

    bool open(const char *path) {
        if (CVI_NN_RegisterModel(path, &handle) != 0) return false;
        return tensors();
    }
    // Program `program` of an open model with several (one recognizer in
    // several widths): it shares the model's weights.
    bool open_program(const Model &model, int program) {
        if (CVI_NN_CloneModel(model.handle, &handle) != 0) return false;
        CVI_NN_SetConfig(handle, OPTION_PROGRAM_INDEX, program);
        return tensors();
    }
    bool tensors() {
        if (CVI_NN_GetInputOutputTensors(handle, &inputs, &input_count, &outputs, &output_count) != 0
            || input_count != 1 || output_count < 1) return false;
        const CVI_SHAPE shape = CVI_NN_TensorShape(&inputs[0]);
        if (shape.dim_size != 4 || shape.dim[0] != 1 || shape.dim[1] != 3) return false;
        in_h = shape.dim[2];
        in_w = shape.dim[3];
        return in_h > 0 && in_w > 0;
    }
    ~Model() { if (handle) CVI_NN_CleanupModel(handle); }
    bool forward() { return CVI_NN_Forward(handle, inputs, input_count, outputs, output_count) == 0; }
};

// Tensor element access for the formats tpu-mlir gives inputs and outputs.
void put(CVI_TENSOR *t, size_t i, float v) {
    void *p = CVI_NN_TensorPtr(t);
    switch (t->fmt) {
    case CVI_FMT_FP32: static_cast<float *>(p)[i] = v; break;
    case CVI_FMT_BF16: {
        uint32_t u;
        std::memcpy(&u, &v, 4);
        static_cast<uint16_t *>(p)[i] = static_cast<uint16_t>((u + 0x8000) >> 16);
        break;
    }
    case CVI_FMT_INT8: {
        const float q = std::nearbyint(v * CVI_NN_TensorQuantScale(t));
        static_cast<int8_t *>(p)[i] = static_cast<int8_t>(std::clamp(q, -128.0f, 127.0f));
        break;
    }
    default: break;
    }
}

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
    default: return 0;
    }
}

bool supported(CVI_TENSOR *t) {
    return t->fmt == CVI_FMT_FP32 || t->fmt == CVI_FMT_BF16 || t->fmt == CVI_FMT_INT8 || t->fmt == CVI_FMT_UINT8;
}

// Bilinear sample of channel c at (fx, fy) in picture coordinates.
float sample(const Picture &pic, float fx, float fy, int c) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(pic.width - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(pic.height - 1));
    const int x0 = static_cast<int>(fx), y0 = static_cast<int>(fy);
    const int x1 = std::min(x0 + 1, pic.width - 1), y1 = std::min(y0 + 1, pic.height - 1);
    const float ax = fx - x0, ay = fy - y0;
    const float top = pic.at(x0, y0, c) * (1 - ax) + pic.at(x1, y0, c) * ax;
    const float bottom = pic.at(x0, y1, c) * (1 - ax) + pic.at(x1, y1, c) * ax;
    return top * (1 - ay) + bottom * ay;
}

// A text region of the detector's map: its bounds in tile pixels, mean
// probability and how many of its pixels each column holds; the line through
// its pixels (mid: its centre at x0) and its thickness across that line.
struct Region {
    int tx, ty, x0, y0, x1, y1;
    float score;
    std::vector<int> column;
    float slope = 0, mid = 0, thick = 0;
};

// The least-squares line through a region's pixels, at most about 11 degrees
// off level, and the region's thickness across it. Short regions (a word or
// two, whose descenders would tilt the fit) and slants under 0.25 degrees
// stay level, as on any screen read straight from HDMI.
void fit_line(Region &r, const std::vector<int> &pixels, int w) {
    double sx = 0, sy = 0;
    for (int i : pixels) { sx += i % w; sy += i / w; }
    const double n = static_cast<double>(pixels.size()), mx = sx / n, my = sy / n;
    double sxx = 0, sxy = 0;
    for (int i : pixels) {
        const double dx = i % w - mx;
        sxx += dx * dx;
        sxy += dx * (i / w - my);
    }
    r.slope = sxx > 0 ? static_cast<float>(std::clamp(sxy / sxx, -0.2, 0.2)) : 0.0f;
    if (r.x1 - r.x0 + 1 < 8 * (r.y1 - r.y0 + 1) || std::fabs(r.slope) < 0.004f) r.slope = 0;
    double lo = 1e9, hi = -1e9;
    for (int i : pixels) {
        const double off = i / w - (my + r.slope * (i % w - mx));
        lo = std::min(lo, off);
        hi = std::max(hi, off);
    }
    r.mid = static_cast<float>(my + r.slope * (r.x0 - mx) + (lo + hi) / 2);
    r.thick = static_cast<float>(hi - lo + 1);
}

// DB detection on one tile at (tx, ty): the regions over threshold.
// false: the TPU failed. That is an error, not a tile without text.
bool detect_tile(Model &det, const Picture &pic, int tx, int ty, std::vector<Region> &regions) {
    const int h = det.in_h, w = det.in_w;
    // PaddleOCR's detector is trained on BGR with ImageNet mean and std.
    static const float mean[3] = {0.485f, 0.456f, 0.406f}, std_[3] = {0.229f, 0.224f, 0.225f};
    CVI_TENSOR *in = &det.inputs[0];
    if (in->fmt == CVI_FMT_UINT8) {
        // Normalization fused into the model: 8-bit BGR planes, outside the
        // picture 0, as the float path below.
        uint8_t *planes = static_cast<uint8_t *>(CVI_NN_TensorPtr(in));
        std::memset(planes, 0, static_cast<size_t>(3) * h * w);
        const int rows = std::min(h, pic.height - ty), cols = std::min(w, pic.width - tx);
        for (int c = 0; c < 3; ++c)
            for (int y = 0; y < rows; ++y)
                std::memcpy(planes + (static_cast<size_t>(c) * h + y) * w,
                    pic.plane(c) + static_cast<size_t>(ty + y) * pic.width + tx, cols);
    } else {
        for (int c = 0; c < 3; ++c) {
            const int src = 2 - c; // BGR from RGB
            for (int y = 0; y < h; ++y)
                for (int x = 0; x < w; ++x) {
                    const int px = tx + x, py = ty + y;
                    const float v = px < pic.width && py < pic.height ? pic.at(px, py, src) : 0;
                    put(in, (static_cast<size_t>(c) * h + y) * w + x, (v / 255.0f - mean[c]) / std_[c]);
                }
        }
    }
    if (!det.forward()) return false;
    CVI_TENSOR *out = &det.outputs[0];
    const CVI_SHAPE shape = CVI_NN_TensorShape(out);
    const int oh = shape.dim[shape.dim_size - 2], ow = shape.dim[shape.dim_size - 1];
    if (oh != h || ow != w) return false;
    std::vector<float> prob(static_cast<size_t>(h) * w);
    if (out->fmt == CVI_FMT_FP32) std::memcpy(prob.data(), CVI_NN_TensorPtr(out), prob.size() * sizeof(float));
    else for (size_t i = 0; i < prob.size(); ++i) prob[i] = get(out, i);
    std::vector<int> label(prob.size(), -1);
    std::vector<int> stack;
    const float bitmap_threshold = 0.3f, box_threshold = 0.6f;
    for (int start = 0; start < static_cast<int>(prob.size()); ++start) {
        if (label[start] >= 0 || prob[start] <= bitmap_threshold) continue;
        // One connected region (8-neighbours): bounding box, score, columns.
        int x0 = w, y0 = h, x1 = -1, y1 = -1, count = 0;
        double sum = 0;
        std::vector<int> pixels;
        stack.assign(1, start);
        label[start] = start;
        while (!stack.empty()) {
            const int i = stack.back();
            stack.pop_back();
            pixels.push_back(i);
            const int x = i % w, y = i / w;
            x0 = std::min(x0, x); x1 = std::max(x1, x); y0 = std::min(y0, y); y1 = std::max(y1, y);
            sum += prob[i];
            ++count;
            for (int dy = -1; dy <= 1; ++dy)
                for (int dx = -1; dx <= 1; ++dx) {
                    const int nx = x + dx, ny = y + dy;
                    if (nx < 0 || ny < 0 || nx >= w || ny >= h) continue;
                    const int n = ny * w + nx;
                    if (label[n] < 0 && prob[n] > bitmap_threshold) { label[n] = start; stack.push_back(n); }
                }
        }
        const int rh = y1 - y0 + 1;
        if (count < 6 || rh < 3 || sum / count < box_threshold) continue;
        Region region{tx, ty, x0, y0, x1, y1, static_cast<float>(sum / count), std::vector<int>(x1 - x0 + 1, 0)};
        for (int i : pixels) ++region.column[i % w - x0];
        fit_line(region, pixels, w);
        regions.push_back(std::move(region));
    }
    return true;
}

// The boxes of a region, unclipped as PaddleOCR does, in picture pixels.
// Recognition reads words and spaces alike, so only a region wider than its
// input (max_ratio) is cut, at its widest run of blank columns, until the
// pieces fit: letters of one word are never parted.
void region_boxes(const Region &r, const Picture &pic, float max_ratio, float unclip, std::vector<Box> &boxes) {
    const float bh = r.thick;
    auto unclip_of = [&](float bw) { return bw * bh * unclip / (2 * (bw + bh)); };
    std::vector<std::pair<int, int>> pieces{{r.x0, r.x1}};
    while (!pieces.empty()) {
        const auto [a, b] = pieces.back();
        pieces.pop_back();
        const float bw = static_cast<float>(b - a + 1), d = unclip_of(bw);
        int gap_start = -1, gap_length = 0;
        if ((bw + 2 * d) / (bh + 2 * d) > max_ratio) {
            for (int c = a, run = 0; c <= b; ++c) {
                run = r.column[c - r.x0] ? 0 : run + 1;
                if (run > gap_length) { gap_length = run; gap_start = c - run + 1; }
            }
        }
        if (gap_length > 0 && gap_start > a && gap_start + gap_length <= b) {
            pieces.push_back({a, gap_start - 1});
            pieces.push_back({gap_start + gap_length, b});
            continue;
        }
        // The top edge at the box's left end, along the region's line.
        const float left = a - d, top = r.mid + r.slope * (left - r.x0) - bh / 2 - d;
        Box box{static_cast<int>(std::lround(r.tx + left)), static_cast<int>(std::lround(r.ty + top)),
                static_cast<int>(bw + 2 * d), static_cast<int>(bh + 2 * d), r.score, r.slope};
        if (box.x < 0) { box.y += static_cast<int>(std::lround(-box.x * box.slope)); box.w += box.x; box.x = 0; }
        box.w = std::min(box.w, pic.width - box.x);
        if (box.w > 2 && box.h > 2 && box.bottom() > 0 && box.top() < pic.height) boxes.push_back(box);
    }
}

// The picture at half size, each pixel the mean of a 2x2 block.
Picture half_size(const Picture &pic) {
    Picture half;
    half.width = pic.width / 2;
    half.height = pic.height / 2;
    half.bgr.resize(static_cast<size_t>(half.width) * half.height * 3);
    for (int c = 0; c < 3; ++c) {
        const uint8_t *src = pic.plane(c);
        uint8_t *dst = &half.bgr[static_cast<size_t>(c) * half.width * half.height];
        for (int y = 0; y < half.height; ++y) {
            const uint8_t *r0 = src + static_cast<size_t>(2 * y) * pic.width, *r1 = r0 + pic.width;
            for (int x = 0; x < half.width; ++x)
                *dst++ = static_cast<uint8_t>((r0[2 * x] + r0[2 * x + 1] + r1[2 * x] + r1[2 * x + 1] + 2) >> 2);
        }
    }
    return half;
}

// Boxes of overlapping tiles: keep the larger of two that mostly overlap.
std::vector<Box> merge(std::vector<Box> boxes) {
    std::sort(boxes.begin(), boxes.end(), [](const Box &a, const Box &b) { return a.w * a.h > b.w * b.h; });
    std::vector<Box> kept;
    for (const Box &b : boxes) {
        bool duplicate = false;
        for (const Box &k : kept) {
            // Along the slant: compare the tops where the two boxes overlap.
            const int x0 = std::max(b.x, k.x), x1 = std::min(b.x + b.w, k.x + k.w);
            const int ix = std::max(0, x1 - x0);
            const float mid = (x0 + x1) / 2.0f;
            const float bt = b.y + b.slope * (mid - b.x), kt = k.y + k.slope * (mid - k.x);
            const float iy = std::max(0.0f, std::min(bt + b.h, kt + k.h) - std::max(bt, kt));
            if (ix * iy > 0.6f * b.w * b.h) { duplicate = true; break; }
        }
        if (!duplicate) kept.push_back(b);
    }
    // Reading order: by the centre line at the left end, then left to right
    // within a row (each centre within half the lower box of the one above it
    // in the row). Two passes: one comparator for both would not be a strict
    // weak order.
    auto centre = [](const Box &b) { return b.y + b.h / 2; };
    std::sort(kept.begin(), kept.end(), [&](const Box &a, const Box &b) { return centre(a) < centre(b); });
    for (size_t row = 0; row < kept.size();) {
        size_t end = row + 1;
        while (end < kept.size() &&
               centre(kept[end]) - centre(kept[end - 1]) <= std::min(kept[end - 1].h, kept[end].h) / 2) ++end;
        std::sort(kept.begin() + row, kept.begin() + end, [](const Box &a, const Box &b) { return a.x < b.x; });
        row = end;
    }
    return kept;
}

// A box wider than the widest recognizer holds at its height is cut into
// pieces that fit, between words: the detector's map runs on through the
// spaces of a line, the picture does not. A column is blank when no pixel in
// it is far from the box's background (its median grey); each cut goes in
// the widest blank run of the right half of the piece, or at its end if
// there is none.
std::vector<Box> split_wide(const Picture &pic, const Box &box, int max_w) {
    if (box.w <= max_w) return {box};
    std::vector<uint8_t> grey(static_cast<size_t>(box.w) * box.h);
    for (int y = 0; y < box.h; ++y)
        for (int x = 0; x < box.w; ++x) {
            const int px = std::min(box.x + x, pic.width - 1);
            const int py = std::clamp(box.y + y + static_cast<int>(std::lround(box.slope * x)), 0, pic.height - 1);
            grey[static_cast<size_t>(y) * box.w + x] = static_cast<uint8_t>(
                (pic.at(px, py, 0) + 2 * pic.at(px, py, 1) + pic.at(px, py, 2) + 2) / 4);
        }
    std::vector<uint8_t> sorted(grey);
    std::nth_element(sorted.begin(), sorted.begin() + sorted.size() / 2, sorted.end());
    const int background = sorted[sorted.size() / 2];
    int contrast = 0;
    for (uint8_t g : grey) contrast = std::max(contrast, std::abs(g - background));
    const int ink_level = std::max(24, contrast / 3);
    std::vector<bool> blank(box.w, true);
    for (int x = 0; x < box.w; ++x)
        for (int y = 0; y < box.h && blank[x]; ++y)
            if (std::abs(grey[static_cast<size_t>(y) * box.w + x] - background) >= ink_level) blank[x] = false;
    std::vector<Box> pieces;
    int start = 0;
    while (box.w - start > max_w) {
        int best = -1, best_run = 0;
        for (int x = start + max_w / 2, run = 0; x < start + max_w; ++x) {
            run = blank[x] ? run + 1 : 0;
            if (run >= best_run && run > 0) { best_run = run; best = x - run / 2; }
        }
        const int cut = best > start ? best : start + max_w;
        pieces.push_back({box.x + start, box.y + static_cast<int>(std::lround(box.slope * start)), cut - start, box.h,
            box.score, box.slope});
        start = cut;
    }
    pieces.push_back({box.x + start, box.y + static_cast<int>(std::lround(box.slope * start)), box.w - start, box.h,
        box.score, box.slope});
    return pieces;
}

// The first largest of n values: its index, and the value in best. Each
// vector lane keeps its largest value and where it was; only whole vectors
// are loaded (RVV 0.7.1 clears the elements past vl, which would lose the
// lanes' values), the rest is scanned one by one. Ties go to the lowest index.
int argmax(const float *p, int n, float &best) {
    int at = 0, i = 0;
    best = p[0];
    const size_t vl = __riscv_vsetvlmax_e32m4();
    if (n >= static_cast<int>(vl)) {
        vfloat32m4_t top = __riscv_vle32_v_f32m4(p, vl);
        vuint32m4_t where = __riscv_vid_v_u32m4(vl);
        vuint32m4_t index = where;
        for (i = static_cast<int>(vl); i + static_cast<int>(vl) <= n; i += static_cast<int>(vl)) {
            index = __riscv_vadd_vx_u32m4(index, static_cast<uint32_t>(vl), vl);
            const vfloat32m4_t v = __riscv_vle32_v_f32m4(p + i, vl);
            const vbool8_t larger = __riscv_vmflt_vv_f32m4_b8(top, v, vl);
            top = __riscv_vmerge_vvm_f32m4(top, v, larger, vl);
            where = __riscv_vmerge_vvm_u32m4(where, index, larger, vl);
        }
        const vfloat32m1_t m = __riscv_vfredmax_vs_f32m4_f32m1(top, __riscv_vfmv_v_f_f32m1(p[0], 1), vl);
        best = __riscv_vfmv_f_s_f32m1_f32(m);
        const vbool8_t is_best = __riscv_vmfeq_vf_f32m4_b8(top, best, vl);
        const vuint32m1_t first = __riscv_vredminu_vs_u32m4_u32m1_m(is_best, where,
            __riscv_vmv_v_x_u32m1(UINT32_MAX, 1), vl);
        at = static_cast<int>(__riscv_vmv_x_s_u32m1_u32(first));
    }
    for (; i < n; ++i)
        if (p[i] > best) { best = p[i]; at = i; }
    return at;
}

// Bilinear sampling of one line into a recognizer input, in fixed point (8
// fractional bits for the weights, 16 for the rows). Per column: the two
// source columns, their weight, and where the line's top is (it follows the
// slant); per output row: each column's two source rows and their weight,
// which serve all three planes.
struct Sampling {
    explicit Sampling(int n) : width(n), x0(n), x1(n), r0(n), r1(n), ax(n), top(n), ay(n) {}
    int width;
    std::vector<uint32_t> x0, x1, r0, r1;
    std::vector<int32_t> ax, top, ay;
};

void sampling_rows(Sampling &s, const Picture &pic, int down) {
    const int32_t max_row = (pic.height - 1) << 16, last = pic.height - 1;
    for (int x = 0; x < s.width;) {
        const size_t vl = __riscv_vsetvl_e32m4(s.width - x);
        vint32m4_t fy = __riscv_vadd_vx_i32m4(__riscv_vle32_v_i32m4(s.top.data() + x, vl), down, vl);
        fy = __riscv_vmax_vx_i32m4(__riscv_vmin_vx_i32m4(fy, max_row, vl), 0, vl);
        const vint32m4_t y0 = __riscv_vsra_vx_i32m4(fy, 16, vl);
        const vint32m4_t y1 = __riscv_vmin_vx_i32m4(__riscv_vadd_vx_i32m4(y0, 1, vl), last, vl);
        __riscv_vse32_v_u32m4(s.r0.data() + x, __riscv_vreinterpret_v_i32m4_u32m4(__riscv_vmul_vx_i32m4(y0, pic.width, vl)), vl);
        __riscv_vse32_v_u32m4(s.r1.data() + x, __riscv_vreinterpret_v_i32m4_u32m4(__riscv_vmul_vx_i32m4(y1, pic.width, vl)), vl);
        __riscv_vse32_v_i32m4(s.ay.data() + x, __riscv_vand_vx_i32m4(__riscv_vsra_vx_i32m4(fy, 8, vl), 255, vl), vl);
        x += static_cast<int>(vl);
    }
}

// One output row of one plane: the vector unit gathers the four source
// pixels of a run of columns at once (th.vlxbu.v loads bytes by index into
// 32-bit elements).
void sample_row(const Sampling &s, const uint8_t *plane, uint8_t *row) {
    const uint32_t *base = reinterpret_cast<const uint32_t *>(plane); // the intrinsic's pointer type
    for (int x = 0; x < s.width;) {
        const size_t vl = __riscv_vsetvl_e32m4(s.width - x);
        const vuint32m4_t r0 = __riscv_vle32_v_u32m4(s.r0.data() + x, vl), r1 = __riscv_vle32_v_u32m4(s.r1.data() + x, vl);
        const vuint32m4_t x0 = __riscv_vle32_v_u32m4(s.x0.data() + x, vl), x1 = __riscv_vle32_v_u32m4(s.x1.data() + x, vl);
        auto pixel = [&](vuint32m4_t r, vuint32m4_t c) {
            return __riscv_vreinterpret_v_u32m4_i32m4(__riscv_th_vlxbu_v_u32m4(base, __riscv_vadd_vv_u32m4(r, c, vl), vl));
        };
        const vint32m4_t a = pixel(r0, x0), b = pixel(r0, x1), d = pixel(r1, x0), e = pixel(r1, x1);
        const vint32m4_t ax = __riscv_vle32_v_i32m4(s.ax.data() + x, vl), ay = __riscv_vle32_v_i32m4(s.ay.data() + x, vl);
        const vint32m4_t top = __riscv_vmacc_vv_i32m4(__riscv_vsll_vx_i32m4(a, 8, vl), __riscv_vsub_vv_i32m4(b, a, vl), ax, vl);
        const vint32m4_t bottom = __riscv_vmacc_vv_i32m4(__riscv_vsll_vx_i32m4(d, 8, vl), __riscv_vsub_vv_i32m4(e, d, vl), ax, vl);
        vint32m4_t v = __riscv_vmacc_vv_i32m4(__riscv_vsll_vx_i32m4(top, 8, vl), __riscv_vsub_vv_i32m4(bottom, top, vl), ay, vl);
        v = __riscv_vadd_vx_i32m4(v, 1 << 15, vl);
        const vuint16m2_t v16 = __riscv_vnsrl_wx_u16m2(__riscv_vreinterpret_v_i32m4_u32m4(v), 16, vl);
        __riscv_vse8_v_u8m1(row + x, __riscv_vnsrl_wx_u8m1(v16, 0, vl), vl);
        x += static_cast<int>(vl);
    }
}

// Recognition of one box: height 48 (the model's), width by the box ratio,
// padded; returns the text, the mean probability of its characters and that
// of the least sure one. Nothing: the TPU failed, unlike an empty text.
std::optional<std::string> recognize(Model &rec, const Picture &pic, const Box &box,
    const std::vector<std::string> &dict, float &score, float &weakest) {
    const int h = rec.in_h, w = rec.in_w;
    const int width = std::clamp(static_cast<int>(std::ceil(static_cast<float>(h) * box.w / box.h)), 1, w);
    CVI_TENSOR *in = &rec.inputs[0];
    const bool raw = in->fmt == CVI_FMT_UINT8;
    uint8_t *planes = raw ? static_cast<uint8_t *>(CVI_NN_TensorPtr(in)) : nullptr;
    if (raw) {
        // 8-bit planes (normalization fused into the model): the bilinear
        // weights of each column are worked out once for all rows and planes.
        // Fixed point, 8 fractional bits for the weights and 16 for the rows;
        // each pixel's two rows and weights serve all three planes.
        Sampling s(width);
        for (int x = 0; x < width; ++x) {
            const float fx = std::clamp(box.x + (x + 0.5f) * box.w / width - 0.5f, 0.0f, pic.width - 1.0f);
            const int x0 = static_cast<int>(fx);
            s.x0[x] = static_cast<uint32_t>(x0);
            s.x1[x] = static_cast<uint32_t>(std::min(x0 + 1, pic.width - 1));
            s.ax[x] = static_cast<int32_t>((fx - x0) * 256 + 0.5f);
            s.top[x] = static_cast<int32_t>(std::lround((box.y + box.slope * (fx - box.x) - 0.5f) * 65536));
        }
        const int step = static_cast<int>(std::lround(static_cast<double>(box.h) / h * 65536));
        for (int y = 0; y < h; ++y) {
            sampling_rows(s, pic, y * step + step / 2);
            for (int c = 0; c < 3; ++c) {
                uint8_t *row = planes + (static_cast<size_t>(c) * h + y) * w;
                sample_row(s, pic.plane(c), row);
                // The padding: grey, 0 after normalization.
                std::memset(row + width, 128, w - width);
            }
        }
    }
    for (int c = 0; !raw && c < 3; ++c) {
        const int src = 2 - c;
        for (int y = 0; y < h; ++y)
            for (int x = 0; x < w; ++x) {
                // The padding is 0 after normalization: grey (128) raw.
                float v = raw ? 128.0f : 0.0f;
                if (x < width) {
                    const float fx = box.x + (x + 0.5f) * box.w / width - 0.5f;
                    const float fy = box.y + box.slope * (fx - box.x) + (y + 0.5f) * box.h / h - 0.5f;
                    v = sample(pic, fx, fy, src);
                    if (!raw) v = (v / 255.0f - 0.5f) / 0.5f;
                }
                const size_t i = (static_cast<size_t>(c) * h + y) * w + x;
                if (raw) planes[i] = static_cast<uint8_t>(std::lround(std::clamp(v, 0.0f, 255.0f)));
                else put(in, i, v);
            }
    }
    score = 0;
    weakest = 1;
    if (!rec.forward()) return std::nullopt;
    CVI_TENSOR *out = &rec.outputs[0];
    const CVI_SHAPE shape = CVI_NN_TensorShape(out);
    // Steps x classes, with trailing 1s as tpu-mlir pads the shape to 4D.
    int last = static_cast<int>(shape.dim_size) - 1;
    while (last > 0 && shape.dim[last] == 1) --last;
    const int classes = shape.dim[last];
    const int steps = static_cast<int>(CVI_NN_TensorCount(out) / classes);
    std::string text;
    int previous = 0, chars = 0;
    // This scan is steps x classes (hundreds to thousands): FP32
    // output, as tpu-mlir gives it, is read directly.
    const float *probs = out->fmt == CVI_FMT_FP32 ? static_cast<const float *>(CVI_NN_TensorPtr(out)) : nullptr;
    for (int t = 0; t < steps; ++t) {
        int best = 0;
        float best_p;
        if (probs) {
            best = argmax(probs + static_cast<size_t>(t) * classes, classes, best_p);
        } else {
            best_p = get(out, static_cast<size_t>(t) * classes);
            for (int k = 1; k < classes; ++k) {
                const float p = get(out, static_cast<size_t>(t) * classes + k);
                if (p > best_p) { best_p = p; best = k; }
            }
        }
        // 0 is the CTC blank; then the dictionary; the last class is a space.
        if (best != 0 && best != previous) {
            if (best <= static_cast<int>(dict.size())) text += dict[best - 1];
            else text += ' ';
            score += best_p;
            weakest = std::min(weakest, best_p);
            ++chars;
        }
        previous = best;
    }
    if (chars) score /= chars;
    return text;
}

std::u32string utf32(const std::string &s) {
    std::u32string out;
    for (size_t i = 0; i < s.size();) {
        const unsigned char c = s[i];
        const int n = c < 0x80 ? 1 : (c >> 5) == 6 ? 2 : (c >> 4) == 14 ? 3 : (c >> 3) == 30 ? 4 : 1;
        char32_t cp = n == 1 ? c : c & (0x7f >> n);
        for (int k = 1; k < n && i + k < s.size(); ++k) cp = (cp << 6) | (static_cast<unsigned char>(s[i + k]) & 0x3f);
        out += cp;
        i += n;
    }
    return out;
}

std::string utf8(const std::u32string &s) {
    std::string out;
    for (const char32_t cp : s) {
        if (cp < 0x80) {
            out += static_cast<char>(cp);
        } else if (cp < 0x800) {
            out += static_cast<char>(0xc0 | (cp >> 6));
            out += static_cast<char>(0x80 | (cp & 0x3f));
        } else if (cp < 0x10000) {
            out += static_cast<char>(0xe0 | (cp >> 12));
            out += static_cast<char>(0x80 | ((cp >> 6) & 0x3f));
            out += static_cast<char>(0x80 | (cp & 0x3f));
        } else {
            out += static_cast<char>(0xf0 | (cp >> 18));
            out += static_cast<char>(0x80 | ((cp >> 12) & 0x3f));
            out += static_cast<char>(0x80 | ((cp >> 6) & 0x3f));
            out += static_cast<char>(0x80 | (cp & 0x3f));
        }
    }
    return out;
}

// Latin and Cyrillic letters of the same shape.
const std::pair<char32_t, char32_t> look_alikes[] = {
    {U'A', U'А'}, {U'B', U'В'}, {U'C', U'С'}, {U'E', U'Е'}, {U'H', U'Н'}, {U'K', U'К'}, {U'M', U'М'},
    {U'O', U'О'}, {U'P', U'Р'}, {U'T', U'Т'}, {U'X', U'Х'}, {U'a', U'а'}, {U'c', U'с'}, {U'e', U'е'},
    {U'o', U'о'}, {U'p', U'р'}, {U'x', U'х'}, {U'y', U'у'}, {U'k', U'к'}, {U'i', U'і'}, {U'I', U'І'},
    {U'j', U'ј'}, {U's', U'ѕ'}, {U'S', U'Ѕ'},
};
// Letters the Cyrillic model reads for one another in words of the other
// script ("пginx", "adтin"), unlike in print.
const std::pair<char32_t, char32_t> confusables[] = {{U'n', U'п'}, {U'm', U'т'}, {U'u', U'и'}, {U'r', U'г'}};

enum class Kind { none, latin, cyrillic, other, twin };

template <size_t N>
char32_t partner(const std::pair<char32_t, char32_t> (&pairs)[N], char32_t c) {
    for (const auto &pair : pairs) {
        if (pair.first == c) return pair.second;
        if (pair.second == c) return pair.first;
    }
    return 0;
}

// Letters only: anything else ends a run of letters.
Kind kind_of(char32_t c) {
    if ((c >= U'a' && c <= U'z') || (c >= U'A' && c <= U'Z') ||
        (c >= 0xc0 && c <= 0x24f && c != 0xd7 && c != 0xf7) || (c >= 0x1e00 && c <= 0x1eff)) return Kind::latin;
    if (c >= 0x400 && c <= 0x4ff) return Kind::cyrillic;
    if ((c >= 0x370 && c <= 0x3ff) || (c >= 0x3040 && c <= 0x30ff) || (c >= 0x3400 && c <= 0x9fff) ||
        (c >= 0xac00 && c <= 0xd7af)) return Kind::other;
    return Kind::none;
}

// The script of the nearest Latin or Cyrillic letters before and after i in
// its run of letters: the one both sides agree on, or the only side there is.
Kind context(const std::vector<Kind> &kinds, size_t i) {
    auto side = [&](int step) {
        for (long j = static_cast<long>(i) + step; j >= 0 && j < static_cast<long>(kinds.size()); j += step) {
            if (kinds[j] == Kind::none) break;
            if (kinds[j] == Kind::latin || kinds[j] == Kind::cyrillic) return kinds[j];
        }
        return Kind::none;
    };
    const Kind left = side(-1), right = side(1);
    if (left == right || right == Kind::none) return left;
    return left == Kind::none ? right : Kind::none;
}

// The Cyrillic model mixes Latin and Cyrillic letters ("USВ", "Wаrning",
// "пgіnх"), and seldom reads spaces, so words cannot decide. A letter that
// has a twin in the other script takes the script of its unambiguous
// neighbours in the run of letters it is in; when they disagree or there are
// none, it stays as read. The confusable letters go first, against
// unambiguous letters only; then they count as letters of their script.
std::string one_script(const std::string &line) {
    std::u32string text = utf32(line);
    std::vector<Kind> kinds(text.size());
    auto settle = [&](auto &pairs) {
        for (size_t i = 0; i < text.size(); ++i) {
            const char32_t other = partner(pairs, text[i]);
            if (!other || kinds[i] != Kind::twin) continue;
            const Kind want = context(kinds, i);
            if (want != Kind::none && kind_of(other) == want) text[i] = other;
        }
    };
    for (size_t i = 0; i < text.size(); ++i)
        kinds[i] = partner(confusables, text[i]) || partner(look_alikes, text[i]) ? Kind::twin : kind_of(text[i]);
    settle(confusables);
    for (size_t i = 0; i < text.size(); ++i)
        kinds[i] = partner(look_alikes, text[i]) ? Kind::twin : kind_of(text[i]);
    // Every look-alike decides against the text as it was before this pass.
    const std::vector<Kind> before = kinds;
    std::u32string out = text;
    for (size_t i = 0; i < text.size(); ++i) {
        const char32_t other = partner(look_alikes, text[i]);
        if (!other) continue;
        const Kind want = context(before, i);
        if (want != Kind::none && kind_of(other) == want) out[i] = other;
    }
    return utf8(out);
}

void json_string(const std::string &s) {
    std::putchar('"');
    for (unsigned char c : s) {
        if (c == '"' || c == '\\') std::printf("\\%c", c);
        else if (c < 0x20) std::printf("\\u%04x", c);
        else std::putchar(c);
    }
    std::putchar('"');
}

// A field of /proc/meminfo or /proc/self/status in KiB, or -1.
long proc_kb(const char *path, const char *field) {
    std::ifstream file(path);
    const size_t n = std::strlen(field);
    for (std::string line; std::getline(file, line);)
        if (line.compare(0, n, field) == 0 && line.size() > n && line[n] == ':') return std::atol(line.c_str() + n + 1);
    return -1;
}

// The dma-buf buffers this process holds (the models and their TPU memory,
// from CMA), in KiB.
long dmabuf_kb() {
    long total = 0;
    DIR *dir = opendir("/proc/self/fd");
    if (!dir) return -1;
    while (const dirent *entry = readdir(dir)) {
        char link[300], target[64] = {};
        std::snprintf(link, sizeof(link), "/proc/self/fd/%s", entry->d_name);
        if (readlink(link, target, sizeof(target) - 1) <= 0 || !std::strstr(target, "dmabuf")) continue;
        std::snprintf(link, sizeof(link), "/proc/self/fdinfo/%s", entry->d_name);
        std::ifstream info(link);
        for (std::string line; std::getline(info, line);)
            if (line.compare(0, 5, "size:") == 0) total += std::atol(line.c_str() + 5) / 1024;
    }
    closedir(dir);
    return total;
}

// Debug: the memory held, models in CMA (dma-buf) and the process's own.
void memory_note(const char *stage) {
    if (!getenv("NANOKVM_OCR_DEBUG")) return;
    std::fprintf(stderr, "memory %s: cma %ld KiB, rss %ld KiB, rss peak %ld KiB\n", stage, dmabuf_kb(),
        proc_kb("/proc/self/status", "VmRSS"), proc_kb("/proc/self/status", "VmHWM"));
}

// The model files of REC.cvimodel[:N][,...]: the program counts dropped.
std::vector<std::string> model_paths(const char *list) {
    std::vector<std::string> paths;
    for (const char *p = list; *p;) {
        const char *comma = std::strchr(p, ',');
        std::string arg(p, comma ? comma - p : std::strlen(p));
        p = comma ? comma + 1 : p + arg.size();
        const size_t colon = arg.rfind(':');
        paths.push_back(colon == std::string::npos ? arg : arg.substr(0, colon));
    }
    return paths;
}

// Has the page cache hold a file, read through a small buffer.
void warm(const std::string &path) {
    const int fd = open(path.c_str(), O_RDONLY | O_CLOEXEC);
    if (fd < 0) return;
    std::vector<char> buffer(1 << 20);
    while (::read(fd, buffer.data(), buffer.size()) > 0) {}
    close(fd);
}

// Takes `bytes` of the TPU's memory (the dma-buf heap the runtime uses: the
// one TPU_DMA_HEAP names, else linux,cma, else the first that is not the
// system heap) until the returned descriptor is closed; -1 when it cannot.
int hold_tpu_memory(size_t bytes) {
    std::vector<std::string> heaps;
    if (const char *name = getenv("TPU_DMA_HEAP")) heaps.push_back(name);
    heaps.push_back("linux,cma");
    if (DIR *dir = opendir("/dev/dma_heap")) {
        while (const dirent *entry = readdir(dir))
            if (entry->d_name[0] != '.' && std::strcmp(entry->d_name, "system") != 0) heaps.push_back(entry->d_name);
        closedir(dir);
    }
    for (const std::string &heap : heaps) {
        const int fd = open(("/dev/dma_heap/" + heap).c_str(), O_RDWR | O_CLOEXEC);
        if (fd < 0) continue;
        dma_heap_allocation_data alloc = {};
        alloc.len = bytes;
        alloc.fd_flags = O_RDWR | O_CLOEXEC;
        const int rc = ioctl(fd, DMA_HEAP_IOCTL_ALLOC, &alloc);
        close(fd);
        return rc == 0 ? static_cast<int>(alloc.fd) : -1;
    }
    return -1;
}

// Model files read into the page cache by a thread of their own while the
// TPU detects, for the runtime to load them from there.
class ModelFiles {
public:
    explicit ModelFiles(const char *list) : paths_(model_paths(list)), reader_([this] {
        for (const std::string &path : paths_) warm(path);
    }) {}
    ~ModelFiles() { wait(); }
    void wait() { if (reader_.joinable()) reader_.join(); }

private:
    std::vector<std::string> paths_;
    std::thread reader_;
};

long ms_since(std::chrono::steady_clock::time_point start) {
    return static_cast<long>(std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::steady_clock::now() - start).count());
}

} // namespace

int main(int argc, char **argv) {
    if (argc != 4 && argc != 5) {
        std::fprintf(stderr, "usage: nanokvm-ocr DET.cvimodel REC.cvimodel[:PROGRAMS][,REC.cvimodel...] DICT.txt"
            " [SURE.cvimodel[:PROGRAMS][,...]] < picture\n");
        return 1;
    }
    memory_note("start");
    std::vector<std::string> dict;
    {
        std::ifstream file(argv[3]);
        for (std::string line; std::getline(file, line);) {
            if (!line.empty() && line.back() == '\r') line.pop_back();
            dict.push_back(line);
        }
    }
    Picture pic;
    char header[64] = {};
    bool planar = false;
    if (!std::fgets(header, sizeof(header), stdin)) header[0] = 0;
    const char *size = header;
    if (header[0] == 'P' && header[1] == ' ') { planar = true; size += 2; }
    if (std::sscanf(size, "%d %d", &pic.width, &pic.height) != 2
        || pic.width < 16 || pic.height < 16 || pic.width > 4096 || pic.height > 4096 || dict.empty()) {
        std::fprintf(stderr, "nanokvm-ocr: invalid picture header or dictionary\n");
        return 1;
    }
    const size_t pixels = static_cast<size_t>(pic.width) * pic.height;
    pic.bgr.resize(pixels * 3);
    std::vector<uint8_t> rgb(planar ? 0 : pixels * 3);
    uint8_t *data = planar ? pic.bgr.data() : rgb.data();
    if (std::fread(data, 1, pixels * 3, stdin) != pixels * 3) {
        std::fprintf(stderr, "nanokvm-ocr: short picture\n");
        return 1;
    }
    for (size_t i = 0; !planar && i < pixels; ++i)
        for (int c = 0; c < 3; ++c) pic.bgr[(2 - c) * pixels + i] = rgb[i * 3 + c];
    // The TPU memory comes from the region that video shares: the detector is
    // freed as soon as detection is done.
    auto load = [](Model &m, const char *path) {
        const auto loading = std::chrono::steady_clock::now();
        if (!m.open(path) || !supported(&m.inputs[0]) || !supported(&m.outputs[0])) {
            std::fprintf(stderr, "nanokvm-ocr: cannot load %s\n", path);
            return false;
        }
        if (getenv("NANOKVM_OCR_DEBUG")) {
            const CVI_SHAPE s = CVI_NN_TensorShape(&m.outputs[0]);
            std::fprintf(stderr, "model in %dx%d fmt %d; out fmt %d [%d %d %d %d]; loaded in %ld ms\n", m.in_w, m.in_h,
                m.inputs[0].fmt, m.outputs[0].fmt, s.dim[0], s.dim[1], s.dim[2], s.dim[3], ms_since(loading));
        }
        return true;
    };
    // Recognition models of one height in several widths, narrowest first.
    // REC.cvimodel:N is one recognizer whose N programs (its widths) share
    // the weights, loaded once, once a ModelFiles has read their files.
    using Recs = std::vector<std::unique_ptr<Model>>;
    auto load_recs = [&](const char *list, Recs &recs, ModelFiles &files) {
        files.wait();
        std::vector<std::string> rec_paths;
        for (const char *p = list; *p;) {
            const char *comma = std::strchr(p, ',');
            rec_paths.emplace_back(p, comma ? comma - p : std::strlen(p));
            p = comma ? comma + 1 : p + rec_paths.back().size();
        }
        for (const std::string &arg : rec_paths) {
            const size_t colon = arg.rfind(':');
            const int programs = colon == std::string::npos ? 1 : std::atoi(arg.c_str() + colon + 1);
            const std::string path = programs > 1 ? arg.substr(0, colon) : arg;
            recs.push_back(std::make_unique<Model>());
            const Model &first = *recs.back();
            if (!load(*recs.back(), path.c_str())) return false;
            for (int program = 1; program < programs; ++program) {
                recs.push_back(std::make_unique<Model>());
                Model &m = *recs.back();
                if (!m.open_program(first, program) || !supported(&m.inputs[0]) || !supported(&m.outputs[0])) {
                    std::fprintf(stderr, "nanokvm-ocr: cannot open program %d of %s\n", program, path.c_str());
                    return false;
                }
            }
        }
        std::sort(recs.begin(), recs.end(), [](const auto &a, const auto &b) { return a->in_w < b->in_w; });
        return true;
    };
    // The memory the detector takes (61.5 MiB for 960x544).
    const size_t det_memory = 64u << 20;
    // Each model is loaded once (a load reads it from the card, 0.25-1.2 s).
    // The detector and the recognizers do not fit in memory together next to
    // the video buffers: the detector is freed before the recognizers load.
    //
    // The model files stay in the page cache from one read to the next only
    // where the TPU's memory will not be taken: taking it drops the cache
    // there (the CMA region is shared with the cache). So the detector's file
    // is read into the cache while that much memory is held, and the
    // recognizers' files while the detector holds it; the detector then takes
    // the memory held and the recognizers the detector's, not the files.
    // The files are read through the cache, not into the process: the
    // copies took the memory the cache needed (the detector's file came
    // from the card each time, about 1 s with a viewer).
    auto start = std::chrono::steady_clock::now();
    const int scale = pic.width > 1280 ? 2 : 1;
    const Picture half = scale == 2 ? half_size(pic) : Picture{};
    const Picture &small = scale == 2 ? half : pic;
    std::vector<Region> regions;
    std::unique_ptr<ModelFiles> rec_files, sure_files;
    {
        {
            const auto holding = std::chrono::steady_clock::now();
            const int held = hold_tpu_memory(det_memory);
            const long hold_ms = ms_since(holding);
            const auto reading = std::chrono::steady_clock::now();
            warm(argv[1]);
            if (held >= 0) close(held);
            if (getenv("NANOKVM_OCR_DEBUG"))
                std::fprintf(stderr, "detector memory %s in %ld ms, file read in %ld ms\n", held >= 0 ? "held" : "not held",
                    hold_ms, ms_since(reading));
        }
        Model det;
        if (!load(det, argv[1])) return 1;
        rec_files = std::make_unique<ModelFiles>(argv[2]);
        if (argc == 5) sure_files = std::make_unique<ModelFiles>(argv[4]);
        // Tiles overlap by a text line or two, so no line is cut in all of them.
        const int overlap = 48;
        for (int ty = 0; ty < small.height; ty += det.in_h - overlap) {
            for (int tx = 0; tx < small.width; tx += det.in_w - overlap) {
                if (!detect_tile(det, small, tx, ty, regions)) {
                    std::fprintf(stderr, "nanokvm-ocr: text detection failed on the TPU\n");
                    return 1;
                }
                if (tx + det.in_w >= small.width) break;
            }
            if (ty + det.in_h >= small.height) break;
        }
        memory_note("detector");
    }
    Recs recs, sure;
    if (!load_recs(argv[2], recs, *rec_files)) return 1;
    rec_files.reset();
    memory_note("recognizers loaded");
    // The widest recognizer sets how wide a box may be.
    const float max_ratio = static_cast<float>(recs.back()->in_w) / recs.back()->in_h;
    // PaddleOCR unclips regions by 1.5; at half size the regions come out
    // thicker relative to the text, and 1.5 joined adjacent lines. With 0.9
    // the boxes match those of the full picture (measured on a browser page:
    // the same lines and text, 0.5-1.1 tried).
    const float unclip = scale == 2 ? 0.9f : 1.5f;
    std::vector<Box> boxes;
    for (const Region &region : regions) region_boxes(region, small, max_ratio, unclip, boxes);
    for (Box &box : boxes) {
        box.x *= scale;
        box.y *= scale;
        box.w = std::min(box.w * scale, pic.width - box.x);
        box.h = std::min(box.h * scale, pic.height - box.y);
    }
    // A slanted box can start at or below the bottom edge: nothing to read.
    boxes.erase(std::remove_if(boxes.begin(), boxes.end(), [](const Box &b) { return b.w <= 0 || b.h <= 0; }),
                boxes.end());
    std::vector<Box> lines;
    for (const Box &box : merge(std::move(boxes)))
        for (const Box &piece : split_wide(pic, box, static_cast<int>(max_ratio * box.h))) lines.push_back(piece);
    boxes = std::move(lines);
    const long det_ms = ms_since(start);
    start = std::chrono::steady_clock::now();
    if (getenv("NANOKVM_OCR_DEBUG")) std::fprintf(stderr, "models and detection %ld ms\n", det_ms);
    // The narrowest model that holds the line at the recognition height: its
    // TPU time follows the width.
    auto fitting = [](const Recs &set, const Box &box) {
        for (const auto &m : set)
            if (static_cast<float>(m->in_h) * box.w / box.h <= m->in_w) return m.get();
        return set.back().get();
    };
    // A line with a doubtful character is read again by the surer (bigger,
    // 4-5 times slower) recognizer, if one is given: on photos of screens
    // that is a fifth to a quarter of the lines and most of its gain. It
    // loads at the first such line.
    const float doubtful = 0.6f;
    int words = 0, reread = 0;
    for (const Box &box : boxes) {
        Model *rec = fitting(recs, box);
        float score = 0, weakest = 0;
        const auto line_start = std::chrono::steady_clock::now();
        std::optional<std::string> read = recognize(*rec, pic, box, dict, score, weakest);
        if (read && argc == 5 && weakest < doubtful) {
            if (sure.empty() && !load_recs(argv[4], sure, *sure_files)) return 1;
            rec = fitting(sure, box);
            read = recognize(*rec, pic, box, dict, score, weakest);
            ++reread;
        }
        // A line the TPU could not read fails the whole read: leaving it out
        // would return part of the text as if it were all.
        if (!read) {
            std::fprintf(stderr, "nanokvm-ocr: text recognition failed on the TPU\n");
            return 1;
        }
        const std::string text = one_script(*read);
        if (getenv("NANOKVM_OCR_DEBUG"))
            std::fprintf(stderr, "box %d,%d %dx%d det %.2f -> '%s' %.3f min %.3f; width %d, %ld ms\n", box.x, box.y, box.w, box.h,
                box.score, text.c_str(), score, weakest, rec->in_w, ms_since(line_start));
        if (text.empty() || score < 0.5f) continue;
        std::printf("{\"text\":");
        json_string(text);
        // The upright rectangle around the line, and its slant.
        std::printf(",\"score\":%.3f,\"x\":%d,\"y\":%d,\"w\":%d,\"h\":%d,\"slope\":%.4f}\n", score, box.x, box.top(), box.w,
            box.bottom() - box.top(), box.slope);
        ++words;
    }
    memory_note("recognition");
    std::printf("{\"done\":true,\"words\":%d,\"reread\":%d,\"det_ms\":%ld,\"rec_ms\":%ld}\n", words, reread, det_ms,
        ms_since(start));
    return 0;
}
