#include <cassert>
#include "stream_size.hpp"
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
 assert(nanokvm::portrait_fps_limit(1080,1920)==70);
 assert(nanokvm::portrait_fps_limit(1296,2304)==50);
 assert(nanokvm::portrait_fps_limit(1440,2560)==40);
 assert(nanokvm::portrait_fps_limit(1920,1080)==0);
}
