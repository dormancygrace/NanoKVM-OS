#include <cassert>
#include "stream_size.hpp"
#include "../../kvm_mmf/include/internal/capture_rate.hpp"
int main(){
 auto s=nanokvm::stream_size(720,1280,0,0);assert(s.width==720&&s.height==1280);
 s=nanokvm::stream_size(1080,1920,1920,1080);assert(s.width==1080&&s.height==1920);
 s=nanokvm::stream_size(1080,1920,1280,720);assert(s.width==720&&s.height==1280);
 s=nanokvm::stream_size(720,1280,1280,720);assert(s.width==720&&s.height==1280);
 s=nanokvm::stream_size(1920,1080,1280,720);assert(s.width==1280&&s.height==720);
 s=nanokvm::stream_size(1440,2560,0,0);assert(s.width==1440&&s.height==2560);
 s=nanokvm::stream_size(1440,2560,1920,1080);assert(s.width==1080&&s.height==1920);
 assert(nanokvm::portrait_fps_limit(720,1280)==120);
 assert(nanokvm::portrait_fps_limit(1080,1920)==75);
 assert(nanokvm::portrait_fps_limit(1296,2304)==0);
 assert(nanokvm::portrait_fps_limit(1440,2560)==50);
 assert(nanokvm::portrait_fps_limit(1920,1080)==0);
 const int tiers[][3] = {{1280,720,120},{1920,1080,75},{1920,1088,75},{2560,1440,50},{2304,1296,50}};
 for (const auto &tier : tiers) {
  assert(nanokvm::capture_rate_limit(tier[0],tier[1])==tier[2]);
  assert(nanokvm::capture_rate_limit(tier[1],tier[0])==tier[2]);
 }
 assert(nanokvm::capture_stream_rate_limit(1440,2560,720,1280)==50);
 assert(nanokvm::capture_stream_rate_limit(1080,1920,720,1280)==75);
 // H.264 runs up to 2560x1440 (1080x1920 in portrait), judged on the encoded size.
 struct H264Case { uint16_t iw,ih,mw,mh; bool allowed; };
 const H264Case h264[] = {
  {3840,2160,0,0,false}, {3840,2160,3840,2160,false}, {3840,2160,2560,1440,true},
  {3840,2160,1920,1080,true}, {3840,2160,1280,720,true}, {3840,2160,800,600,true},
  {2560,1440,0,0,true}, {2560,1440,3840,2160,true}, {1920,1080,0,0,true},
  {3440,1440,0,0,false}, {3440,1440,2560,1440,true},
  {1440,2560,0,0,false}, {1440,2560,3840,2160,false}, {1440,2560,2560,1440,false},
  {1440,2560,1920,1080,true}, {1440,2560,1280,720,true},
  {1088,1920,2560,1440,true},
  {1088,1920,0,0,true}, {1080,1920,0,0,true}, {720,1280,0,0,true},
 };
 for (const auto &c : h264)
  assert(nanokvm::h264_output_allowed(nanokvm::stream_size(c.iw,c.ih,c.mw,c.mh))==c.allowed);
 s=nanokvm::stream_size(3440,1440,2560,1440);assert(s.width==2560&&s.height==1070);
 assert(nanokvm::h264_output_allowed({2560,1440}) && nanokvm::h264_output_allowed({1080,1920}));
 assert(!nanokvm::h264_output_allowed({2562,1440}) && !nanokvm::h264_output_allowed({1080,1922}));
 assert(!nanokvm::h264_output_allowed({1296,2304}));

}
