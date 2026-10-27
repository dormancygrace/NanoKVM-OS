// SPDX-License-Identifier: GPL-3.0-or-later
// nanokvm-ocr: the text on a picture of the screen, with PaddleOCR models
// (PP-OCRv5 text detection and recognition) on the CV181x TPU.
//
//   nanokvm-ocr DET.cvimodel REC.cvimodel DICT.txt < picture
//
// Several recognition models of one height may be given, comma-separated:
// each line goes through the narrowest that holds it.
//
// stdin: "WIDTH HEIGHT\n", then WIDTH*HEIGHT*3 bytes of packed RGB, or
// "P WIDTH HEIGHT\n", then the B, G and R planes (WIDTH*HEIGHT bytes each),
// as VPSS produces them and as the models take them.
// stdout: one JSON object per word, then a summary:
//   {"text":"...","score":0.97,"x":10,"y":20,"w":80,"h":16}
//   {"done":true,"words":12,"det_ms":310,"rec_ms":240}
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
#include <unistd.h>

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <memory>
#include <string>
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
void detect_tile(Model &det, const Picture &pic, int tx, int ty, std::vector<Region> &regions) {
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
    if (!det.forward()) return;
    CVI_TENSOR *out = &det.outputs[0];
    const CVI_SHAPE shape = CVI_NN_TensorShape(out);
    const int oh = shape.dim[shape.dim_size - 2], ow = shape.dim[shape.dim_size - 1];
    if (oh != h || ow != w) return;
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
    // Reading order: by the centre line at the left end, then left to right.
    std::sort(kept.begin(), kept.end(), [](const Box &a, const Box &b) {
        return a.y + a.h / 2 < b.y || (std::abs((a.y + a.h / 2) - (b.y + b.h / 2)) <= std::min(a.h, b.h) / 2 && a.x < b.x);
    });
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

// Recognition of one box: height 48 (the model's), width by the box ratio,
// padded; returns the text and the mean probability of its characters.
std::string recognize(Model &rec, const Picture &pic, const Box &box, const std::vector<std::string> &dict, float &score) {
    const int h = rec.in_h, w = rec.in_w;
    const int width = std::clamp(static_cast<int>(std::ceil(static_cast<float>(h) * box.w / box.h)), 1, w);
    CVI_TENSOR *in = &rec.inputs[0];
    const bool raw = in->fmt == CVI_FMT_UINT8;
    uint8_t *planes = raw ? static_cast<uint8_t *>(CVI_NN_TensorPtr(in)) : nullptr;
    for (int c = 0; c < 3; ++c) {
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
    if (!rec.forward()) return {};
    CVI_TENSOR *out = &rec.outputs[0];
    const CVI_SHAPE shape = CVI_NN_TensorShape(out);
    // Steps x classes, with trailing 1s as tpu-mlir pads the shape to 4D.
    int last = static_cast<int>(shape.dim_size) - 1;
    while (last > 0 && shape.dim[last] == 1) --last;
    const int classes = shape.dim[last];
    const int steps = static_cast<int>(CVI_NN_TensorCount(out) / classes);
    std::string text;
    int previous = 0, chars = 0;
    for (int t = 0; t < steps; ++t) {
        int best = 0;
        float best_p = get(out, static_cast<size_t>(t) * classes);
        for (int k = 1; k < classes; ++k) {
            const float p = get(out, static_cast<size_t>(t) * classes + k);
            if (p > best_p) { best_p = p; best = k; }
        }
        // 0 is the CTC blank; then the dictionary; the last class is a space.
        if (best != 0 && best != previous) {
            if (best <= static_cast<int>(dict.size())) text += dict[best - 1];
            else text += ' ';
            score += best_p;
            ++chars;
        }
        previous = best;
    }
    if (chars) score /= chars;
    return text;
}

// The Cyrillic model mixes letters that look alike in both scripts ("Bид",
// "yстpойство"). A word takes the script most of its letters are in; a word
// with as many of each stays as it is.
std::string word_script(const std::string &word) {
    // Latin and Cyrillic glyphs of the same shape, as UTF-8.
    static const char *const pairs[][2] = {
        {"A", "А"}, {"B", "В"}, {"C", "С"}, {"E", "Е"}, {"H", "Н"}, {"K", "К"}, {"M", "М"}, {"O", "О"},
        {"P", "Р"}, {"T", "Т"}, {"X", "Х"}, {"a", "а"}, {"c", "с"}, {"e", "е"}, {"o", "о"}, {"p", "р"},
        {"x", "х"}, {"y", "у"},
    };
    int latin = 0, cyrillic = 0;
    for (size_t i = 0; i < word.size(); ++i) {
        const unsigned char c = word[i];
        if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) ++latin;
        else if ((c == 0xd0 || c == 0xd1) && i + 1 < word.size()) { ++cyrillic; ++i; }
    }
    if (latin == 0 || cyrillic == 0 || latin == cyrillic) return word;
    const int from = cyrillic > latin ? 0 : 1, to = 1 - from;
    std::string out;
    for (size_t i = 0; i < word.size();) {
        bool replaced = false;
        for (const auto &pair : pairs) {
            const size_t n = std::strlen(pair[from]);
            if (word.compare(i, n, pair[from]) == 0) {
                out += pair[to];
                i += n;
                replaced = true;
                break;
            }
        }
        if (!replaced) out += word[i++];
    }
    return out;
}

std::string one_script(const std::string &line) {
    std::string out;
    for (size_t start = 0; start <= line.size();) {
        const size_t space = std::min(line.find(' ', start), line.size());
        out += word_script(line.substr(start, space - start));
        if (space < line.size()) out += ' ';
        start = space + 1;
    }
    return out;
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
        char link[64], target[64] = {};
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

long ms_since(std::chrono::steady_clock::time_point start) {
    return static_cast<long>(std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::steady_clock::now() - start).count());
}

} // namespace

int main(int argc, char **argv) {
    if (argc != 4) {
        std::fprintf(stderr, "usage: nanokvm-ocr DET.cvimodel REC.cvimodel[,REC.cvimodel...] DICT.txt < picture\n");
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
    std::vector<std::string> rec_paths;
    for (const char *p = argv[2]; *p;) {
        const char *comma = std::strchr(p, ',');
        rec_paths.emplace_back(p, comma ? comma - p : std::strlen(p));
        p = comma ? comma + 1 : p + rec_paths.back().size();
    }
    std::vector<std::unique_ptr<Model>> recs;
    auto load_recs = [&]() {
        for (const std::string &path : rec_paths) {
            recs.push_back(std::make_unique<Model>());
            if (!load(*recs.back(), path.c_str())) return false;
        }
        std::sort(recs.begin(), recs.end(), [](const auto &a, const auto &b) { return a->in_w < b->in_w; });
        return true;
    };
    // Each model is loaded once (a load reads it from the card, 0.25-0.8 s).
    // The detector and the recognizers do not fit in memory together next to
    // the video buffers: the detector is freed before the recognizers load.
    auto start = std::chrono::steady_clock::now();
    const int scale = pic.width > 1280 ? 2 : 1;
    const Picture half = scale == 2 ? half_size(pic) : Picture{};
    const Picture &small = scale == 2 ? half : pic;
    std::vector<Region> regions;
    {
        Model det;
        if (!load(det, argv[1])) return 1;
        // Tiles overlap by a text line or two, so no line is cut in all of them.
        const int overlap = 48;
        for (int ty = 0; ty < small.height; ty += det.in_h - overlap) {
            for (int tx = 0; tx < small.width; tx += det.in_w - overlap) {
                detect_tile(det, small, tx, ty, regions);
                if (tx + det.in_w >= small.width) break;
            }
            if (ty + det.in_h >= small.height) break;
        }
        memory_note("detector");
    }
    if (!load_recs()) return 1;
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
    std::vector<Box> lines;
    for (const Box &box : merge(std::move(boxes)))
        for (const Box &piece : split_wide(pic, box, static_cast<int>(max_ratio * box.h))) lines.push_back(piece);
    boxes = std::move(lines);
    const long det_ms = ms_since(start);
    start = std::chrono::steady_clock::now();
    if (getenv("NANOKVM_OCR_DEBUG")) std::fprintf(stderr, "models and detection %ld ms\n", det_ms);
    int words = 0;
    for (const Box &box : boxes) {
        // The narrowest model that holds the line at the recognition height:
        // its TPU time follows the width.
        Model *rec = recs.back().get();
        for (const auto &m : recs) {
            if (static_cast<float>(m->in_h) * box.w / box.h <= m->in_w) { rec = m.get(); break; }
        }
        float score = 0;
        const auto line_start = std::chrono::steady_clock::now();
        const std::string text = one_script(recognize(*rec, pic, box, dict, score));
        if (getenv("NANOKVM_OCR_DEBUG"))
            std::fprintf(stderr, "box %d,%d %dx%d det %.2f -> '%s' %.3f; width %d, %ld ms\n", box.x, box.y, box.w, box.h,
                box.score, text.c_str(), score, rec->in_w, ms_since(line_start));
        if (text.empty() || score < 0.5f) continue;
        std::printf("{\"text\":");
        json_string(text);
        // The upright rectangle around the line, and its slant.
        std::printf(",\"score\":%.3f,\"x\":%d,\"y\":%d,\"w\":%d,\"h\":%d,\"slope\":%.4f}\n", score, box.x, box.top(), box.w,
            box.bottom() - box.top(), box.slope);
        ++words;
    }
    memory_note("recognition");
    std::printf("{\"done\":true,\"words\":%d,\"det_ms\":%ld,\"rec_ms\":%ld}\n", words, det_ms, ms_since(start));
    return 0;
}
