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
 s=nanokvm::stream_size(1296,2304,0,0);assert(s.width==1296&&s.height==2304);
 s=nanokvm::stream_size(1296,2304,1280,720);assert(s.width==720&&s.height==1280);
 assert(nanokvm::portrait_fps_limit(720,1280)==120);
 assert(nanokvm::portrait_fps_limit(1080,1920)==75);
 assert(nanokvm::portrait_fps_limit(1296,2304)==50);
 assert(nanokvm::portrait_fps_limit(1440,2560)==50);
 assert(nanokvm::portrait_fps_limit(1920,1080)==0);
 const int tiers[][3] = {{1280,720,120},{1920,1080,75},{1920,1088,75},{2560,1440,50},{2304,1296,50}};
 for (const auto &tier : tiers) {
  assert(nanokvm::capture_rate_limit(tier[0],tier[1])==tier[2]);
  assert(nanokvm::capture_rate_limit(tier[1],tier[0])==tier[2]);
 }
 assert(nanokvm::capture_stream_rate_limit(1440,2560,720,1280)==50);
 assert(nanokvm::capture_stream_rate_limit(1080,1920,720,1280)==75);

}
