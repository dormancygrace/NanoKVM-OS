#include "config.h"
#include "system_init.h"

#include <errno.h>
#include <sys/stat.h>

using namespace maix;
using namespace maix::sys;

extern kvm_sys_state_t kvm_sys_state;
extern kvm_oled_state_t kvm_oled_state;

uint8_t get_hdmi_version()
{
	FILE *fp;
	uint8_t RW_Data[2];
    system("/kvmapp/system/init.d/S15kvmhwd get_hdmi_version");
	if(access("/etc/kvm/hdmi_version", F_OK) == 0){
        fp = fopen("/etc/kvm/hdmi_version", "r");
        fread(RW_Data, sizeof(char), 2, fp);
        fclose(fp);
        if(RW_Data[0] == 'u'){
            // 6911uxc / 6911uxe
            if(RW_Data[1] == 'e'){
                return 2;
            } else if(RW_Data[1] == 'x') {
                return 1;
            } else {
                return 1;
            }
        } else if(RW_Data[0] == 'd'){
            // 6911d
            return 3;
        } else {
            // 6911c
            return 0;
        }
    } else {
		return 0;
    }
}

void Production_testing_patch(void)
{	
	// Product UE version detecte
	if(get_hdmi_version() == 2){
		printf("ue_patch_state = 1;\n");
		kvm_oled_state.ue_patch_state = 1;
	} else {
		printf("ue_patch_state = 0;\n");
		kvm_oled_state.ue_patch_state = 0;
	}

	// New products default to disabling mDNS functionality
	system("rm -f /etc/init.d/S50ssdpd");
	system("sync");
}
