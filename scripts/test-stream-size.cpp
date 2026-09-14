#include "../support/sg2002/additional/kvm/include/stream_size.hpp"
#include <cassert>
#include <initializer_list>
int main() {
 struct Case { unsigned iw,ih,mw,mh,ow,oh; };
 for (auto c : {Case{2560,1440,1920,1080,1920,1080}, {1920,1080,1280,720,1280,720},
      {640,480,1920,1080,640,480}, {1024,768,1280,720,960,720}, {1280,1024,1280,720,900,720},
      {2560,1440,0,0,2560,1440}, {1920,1080,2560,1440,1920,1080}, {0,0,1920,1080,0,0},
      {1088,1920,0,0,1088,1920}, {1088,1920,1920,1080,1088,1920},
      {1088,1920,2560,1440,1088,1920},
      {1920,2688,0,0,1920,2688}, {1920,2688,1920,1080,1024,1432}, {1088,1920,1280,720,704,1242}}) {
   auto s=nanokvm::stream_size(c.iw,c.ih,c.mw,c.mh); assert(s.width==c.ow && s.height==c.oh);
 }
 assert(!nanokvm::above_fhd(1088,1920));
 assert(!nanokvm::above_fhd(1920,1080));
 assert(nanokvm::above_fhd(2560,1440));
 assert(nanokvm::above_fhd(1440,2560));
 assert(nanokvm::above_fhd(1152,1920));
}
