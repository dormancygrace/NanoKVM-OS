/* Opus kernels for the T-Head C906: XTheadVector through GCC's RVV intrinsics, float build.
 * scripts/build-usb-audio.py force-includes this file into every Opus source; the OVERRIDE_* macros make Opus
 * use these functions instead of its C loops (its own hook for per-architecture code).
 *
 * Every vector loop runs on whole vectors of 16 floats and the rest is scalar, so nothing depends on what an
 * instruction leaves in the lanes beyond its length. */
#ifndef NANOKVM_OPUS_C906_H
#define NANOKVM_OPUS_C906_H
#include <riscv_vector.h>

#define NK_LANES 16 /* 32-bit elements, LMUL 4, VLEN 128 */

static inline float nk_sum16(vfloat32m4_t v)
{
   vfloat32m1_t zero = __riscv_vfmv_v_f_f32m1(0.f, 4);
   return __riscv_vfmv_f_s_f32m1_f32(__riscv_vfredusum_vs_f32m4_f32m1(v, zero, NK_LANES));
}

/* max(|x[i]|) over a frame: the encoder asks for it on every frame's samples. The value is the C loop's for
 * every input without NaN (the samples come from 16-bit PCM). */
#define OVERRIDE_CELT_MAXABS16
static inline float celt_maxabs16(const float *x, int len)
{
   float maxval = 0, minval = 0;
   int i = 0;
   if (len >= NK_LANES) {
      vfloat32m4_t hi = __riscv_vfmv_v_f_f32m4(0.f, NK_LANES), lo = hi;
      for (; i + NK_LANES <= len; i += NK_LANES) {
         vfloat32m4_t v = __riscv_vle32_v_f32m4(x + i, NK_LANES);
         hi = __riscv_vfmax_vv_f32m4(hi, v, NK_LANES);
         lo = __riscv_vfmin_vv_f32m4(lo, v, NK_LANES);
      }
      vfloat32m1_t zero = __riscv_vfmv_v_f_f32m1(0.f, 4);
      maxval = __riscv_vfmv_f_s_f32m1_f32(__riscv_vfredmax_vs_f32m4_f32m1(hi, zero, NK_LANES));
      minval = __riscv_vfmv_f_s_f32m1_f32(__riscv_vfredmin_vs_f32m4_f32m1(lo, zero, NK_LANES));
   }
   for (; i < len; i++) {
      maxval = maxval > x[i] ? maxval : x[i];
      minval = minval < x[i] ? minval : x[i];
   }
   return maxval > -minval ? maxval : -minval;
}

/* Inner products of 32 elements and more are summed in 16 lanes: the last bits differ from the C loop's single
 * running sum (the decoded sound differs from the C build's by -80 dB and less). */
#define OVERRIDE_CELT_INNER_PROD
#define OVERRIDE_DUAL_INNER_PROD
static inline float nk_inner_prod(const float *x, const float *y, int N)
{
   float xy = 0;
   int i = 0;
   if (N >= 2 * NK_LANES) {
      vfloat32m4_t acc = __riscv_vfmv_v_f_f32m4(0.f, NK_LANES);
      for (; i + NK_LANES <= N; i += NK_LANES)
         acc = __riscv_vfmacc_vv_f32m4(acc, __riscv_vle32_v_f32m4(x + i, NK_LANES), __riscv_vle32_v_f32m4(y + i, NK_LANES), NK_LANES);
      xy = nk_sum16(acc);
   }
   for (; i < N; i++) xy += x[i] * y[i];
   return xy;
}
static inline void nk_dual_inner_prod(const float *x, const float *y01, const float *y02, int N, float *xy1, float *xy2)
{
   float s1 = 0, s2 = 0;
   int i = 0;
   if (N >= 2 * NK_LANES) {
      vfloat32m4_t a = __riscv_vfmv_v_f_f32m4(0.f, NK_LANES), b = a;
      for (; i + NK_LANES <= N; i += NK_LANES) {
         vfloat32m4_t v = __riscv_vle32_v_f32m4(x + i, NK_LANES);
         a = __riscv_vfmacc_vv_f32m4(a, v, __riscv_vle32_v_f32m4(y01 + i, NK_LANES), NK_LANES);
         b = __riscv_vfmacc_vv_f32m4(b, v, __riscv_vle32_v_f32m4(y02 + i, NK_LANES), NK_LANES);
      }
      s1 = nk_sum16(a);
      s2 = nk_sum16(b);
   }
   for (; i < N; i++) {
      s1 += x[i] * y01[i];
      s2 += x[i] * y02[i];
   }
   *xy1 = s1;
   *xy2 = s2;
}
#define celt_inner_prod(x, y, N, arch) ((void)(arch), nk_inner_prod(x, y, N))
#define dual_inner_prod(x, y01, y02, N, xy1, xy2, arch) ((void)(arch), nk_dual_inner_prod(x, y01, y02, N, xy1, xy2))

#endif
