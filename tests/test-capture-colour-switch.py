#!/usr/bin/env python3
"""Execute the actual read-path colour gate with injected switch outcomes."""
from pathlib import Path
import subprocess
import tempfile
repo = Path(__file__).resolve().parents[1]
s = (repo / 'support/additional/kvm/src/kvm_vision.cpp').read_text()
start = s.index('        if (capture_format == nanokvm::nv21_format()) {', s.index('// The video encoders read limited-range'))
body = s[start:s.index('        // MJPEG\'s own output next to a video stream:', start)]
prefix = r'''#include <cassert>
#include <pthread.h>
namespace nanokvm { int nv21_format() { return 1; } }
struct Camera { int get_channel() { return 2; } } camera, *cam=&camera;
struct { int fresh_frame_count; } kvmv_cfg;
static pthread_mutex_t vi_mutex = PTHREAD_MUTEX_INITIALIZER;
static bool active, requested;
static int result, calls;
constexpr int VENC_MJPEG=0, IMG_VENC_ERROR=-4;
static bool mjpeg_video_reader_active(int) { return active; }
static int mmf_vi_chn_set_jfif(int ch,bool on) { assert(ch==2);requested=on;++calls;return result; }
int read_gate(int capture_format,int _type) {
 int capture_now=0;
 pthread_mutex_lock(&vi_mutex);
'''
suffix = r'''
 pthread_mutex_unlock(&vi_mutex);return 0;
}
int main() {
 for (int outcome : {-1,0,1}) for (bool video : {false,true}) for(int codec : {0,1}) {
  result=outcome;active=video;calls=0;kvmv_cfg.fresh_frame_count=0;
  assert(read_gate(1,codec)==(outcome<0 ? IMG_VENC_ERROR : 0));
  assert(calls==1 && requested==(codec==0 && !video));
  assert(kvmv_cfg.fresh_frame_count==(outcome>0 ? 1 : 0));
  assert(pthread_mutex_trylock(&vi_mutex)==0);pthread_mutex_unlock(&vi_mutex);
 }
 calls=0;assert(read_gate(2,0)==0 && calls==0);
 result=1;kvmv_cfg.fresh_frame_count=3;assert(read_gate(1,0)==0 && kvmv_cfg.fresh_frame_count==3);
}
'''
with tempfile.TemporaryDirectory() as tmp:
    p = Path(tmp) / 'check.cpp'
    p.write_text('#include <initializer_list>\n' + prefix + body + suffix)
    binary = p.with_suffix('')
    subprocess.run(['g++','-std=c++17','-Wall','-Wextra','-Werror','-pthread',str(p),'-o',str(binary)], check=True)
    subprocess.run([str(binary)], check=True)
print('Capture colour gate: failures, mixed readers, no-op and frame discard passed')
