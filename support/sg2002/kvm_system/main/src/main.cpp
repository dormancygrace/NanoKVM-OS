#include "config.h"

kvm_sys_state_t kvm_sys_state;
kvm_oled_state_t kvm_oled_state;

void* thread_oled_handle(void *)
{
	OLED_Init();
	OLED_ColorTurn(0);		//0正常显示 1 反色显示
	// The PCIe panel needs A0/C0; the roaming renderer no longer calls
	// kvm_init_pcie_ui(), which previously selected this orientation.
	if (kvm_hw_ver == 2) OLED_Revolve();
	else OLED_DisplayTurn(0);	//0正常显示 1 屏幕翻转显示
	OLED_Clear();
	if(kvm_oled_state.ue_patch_state == 1){
		kvm_show_UE();
		while(kvm_sys_state.oled_thread_running){
            oled_auto_sleep();
			kvms::sleep_ms(100);
		}
		OLED_Clear();
	}

    while(kvm_sys_state.oled_thread_running)
    {
		oled_auto_sleep();
        if (OLED_IsDisabled() && !OLED_IPWindowActive()) { kvms::sleep_ms(OLED_DELAY); continue; }
		// printf("[kvmd]thread_oled_handle - while\n");
		uint8_t page_changed = (kvm_oled_state.page == kvm_sys_state.page)? 0:1;
		uint8_t subpage_changed = (kvm_oled_state.sub_page == kvm_sys_state.sub_page)? 0:1;
		// printf("subpage_changed = %d", subpage_changed);
		// printf("kvm_oled_state.sub_page = %d", kvm_oled_state.sub_page);
		// printf("kvm_sys_state.sub_page = %d\n", kvm_sys_state.sub_page);
		kvm_oled_state.page = kvm_sys_state.page;
		kvm_oled_state.sub_page = kvm_sys_state.sub_page;


		{
			// One screen update, sent in as few I2C transfers as possible.
			OledBatch batch;
			switch(kvm_oled_state.page){
				case 0 : // main page
					kvm_main_ui_disp(page_changed, subpage_changed);
					break;
				case 1 : // wifi config page
					kvm_wifi_config_ui_disp(page_changed, subpage_changed);
					break;
				default:
					OLED_Clear();
			}
		}
		kvms::sleep_ms(OLED_DELAY);
    }
	OLED_Clear();
	kvm_sys_state.oled_thread_running = -1;
	return nullptr;
}

void* thread_key_handle(void *)
{
	uint64_t press_time = 0;
	bool key_pressed = false;
	uint32_t press_cycle;
	int fd = open ("/dev/input/event0", O_RDONLY);
	if (fd == -1) {
		kvm_sys_state.key_thread_running = 0;
	}
	struct input_event event;

    while(kvm_sys_state.key_thread_running)
    {
        if (read(fd, &event, sizeof(event)) != sizeof(event)) break;
		if (event.type == EV_KEY) {
			if (event.value == 1){
				// printf ("[kvmk]按键按下\n");
				press_time = kvms::ticks_ms();
                key_pressed = true;
			} else if (event.value == 0){
                if (!key_pressed) continue;
                key_pressed = false;
				oled_auto_sleep_time_update();
				// printf ("[kvmk]按键抬起\n");
				press_cycle = kvms::ticks_ms() - press_time;
				if(press_cycle >= KEY_LONGLONG_PRESS){
					kvm_reset_password();
				} else if (press_cycle >= KEY_LONG_PRESS && press_cycle < KEY_LONGLONG_PRESS){
					// long
					// printf ("[kvmk]按键长按\n");
					// printf("[kvmk]wifi_state = %d\n", kvm_sys_state.wifi_state);
					if(kvm_sys_state.wifi_state == -2){
						kvm_sys_state.page = 0;
						kvm_sys_state.sub_page = 0;
						continue;
					}
					switch(kvm_sys_state.page){
						case 0:	// main page
							kvm_sys_state.page = 1;
							kvm_sys_state.sub_page = 0;
							break;
						case 1:	// wifi coonfig page
							system("/etc/init.d/S30wifi restart");
							kvm_sys_state.page = 0;
							kvm_sys_state.sub_page = 0;
							kvm_sys_state.wifi_config_process = -1;
							break;
					}
				} else {
					// short
					// printf ("[kvmk]按键短按\n");
					// printf ("[kvmk]wifi_config_process = %d\n", kvm_sys_state.wifi_config_process);
					// printf ("[kvmk]page = %d\n", kvm_sys_state.page);
					// printf ("[kvmk]sub_page = %d\n", kvm_sys_state.sub_page);
					switch(kvm_sys_state.page){
						case 0:	// main page
							if(kvm_sys_state.sub_page == 0) kvm_sys_state.sub_page = 1;
							else kvm_sys_state.sub_page = 0;
							break;
						case 1:	// wifi coonfig page
							switch(kvm_sys_state.wifi_config_process){
								case 1:	// QR1<->TEXT2
									if(kvm_sys_state.sub_page == 1) kvm_sys_state.sub_page = 2;
									else kvm_sys_state.sub_page = 1;
									// printf("[kvmk]sub_page = %d\n", kvm_sys_state.sub_page);
									break;
								case 2:
									if(kvm_sys_state.sub_page == 1) kvm_sys_state.sub_page = 2;
									else if(kvm_sys_state.sub_page == 2) kvm_sys_state.sub_page = 3;
									else if(kvm_sys_state.sub_page == 3) kvm_sys_state.sub_page = 4;
									else kvm_sys_state.sub_page = 1;
									break;
							}
							break;
					}
				}
			}
		}
		kvms::sleep_ms(KEY_DELAY);
    }
	kvm_sys_state.key_thread_running = 0;
    if (fd >= 0) close(fd);
    return nullptr;
}

// The Ethernet, USB, HDMI and stream state is only drawn on the OLED's main
// page. It is not polled (nor the gateway pinged) while nothing shows it: no
// panel, the display asleep or switched off (oled_sleep), or the IP window
// in its place. On waking, the next pass (STATE_DELAY) refreshes it; the HDMI
// flag and the gateway ping follow at their own, slower intervals.
static bool oled_shows_state(void)
{
	return kvm_sys_state.oled_thread_running == 1 && !kvm_oled_state.oled_sleep_state &&
		!OLED_IPWindowActive();
}

void* thread_sys_handle(void *)
{
	get_ping_allow_state();
    while(kvm_sys_state.sys_thread_running)
    {
		// Observe direct IPv4 assignments on every page. The existing state
		// updates are intentionally limited to the main page, but an address
		// acquired while viewing the Wi-Fi page must still open its time window.
		kvm_observe_oled_ip_window();
		// net
		if(kvm_sys_state.page == 0){
			// The Wi-Fi state is also the server's (/run/nanokvm/wifi_state,
			// /etc/kvm/wifi_exist): always kept up to date.
			kvm_update_wifi_state();
			if (oled_shows_state()) {
				kvm_update_eth_state();
				// kvm_update_rndis_state();
				// kvm_update_tailscale_state();
				// sys_state
				kvm_update_usb_state();
				kvm_update_hdmi_state();
				kvm_update_stream_fps();
				kvm_update_hdmi_res();
				kvm_update_stream_type();
				kvm_update_stream_qlty();
			}
			kvm_wifi_web_config_process();

		} else if(kvm_sys_state.page == 1){
			kvm_wifi_config_process();
		}

		kvms::sleep_ms(STATE_DELAY);
    }
	kvm_sys_state.sys_thread_running = 0;
	return nullptr;
}

int main(int argc, char* argv[])
{
#ifdef NANOKVM_OS
    if (argc == 2 && !strcmp(argv[1], "--version")) {
        puts("NanoKVM OS kvm_system / GCC " __VERSION__);
        return 0;
    }
    if (argc == 2 && !strcmp(argv[1], "--self-test")) {
        const uint64_t start = kvms::ticks_ms();
        kvms::sleep_ms(20);
        const uint64_t elapsed = kvms::ticks_ms() - start;
        if (elapsed < 15) return 1;
        const auto addresses = kvms::ip_address();
        const auto wlan = addresses.find("wlan0");
        const std::string expected = wlan == addresses.end() ? "" : wlan->second;
        if (get_ip_addr(WiFi_IP) != static_cast<int>(!expected.empty()) ||
            strcmp(reinterpret_cast<char *>(kvm_sys_state.wifi_addr), expected.c_str())) return 1;
        puts("PASS kvm_system: timer, IPv4 lookup/copy; no board control");
        return 0;
    }

#endif
    // Catch SIGINT signal(e.g. Ctrl + C), and set exit flag to true.
    signal(SIGINT, [](int){ 
	kvm_sys_state.oled_thread_running = 0;
	kvm_sys_state.key_thread_running = 0;
	kvm_sys_state.sys_thread_running = 0;
	// Only async-signal-safe calls here.
	static const char msg[] = "[kvms]Prepare to exit\n";
	ssize_t written = write(STDOUT_FILENO, msg, sizeof(msg) - 1);
	(void)written;
	});	

	pthread_t sys_state_thread;
	pthread_t display_thread;
	bool display_thread_started = false;
	pthread_t key_thread;

	// only for production testing
	if (access("/tmp/S49kvmtest", F_OK) == 0){
		printf("Production testing patch\n");
		Production_testing_patch();
	}

	system("sync");

	OLED_state = oled_exist();
	if(OLED_state){
		printf("oled exist\r\n");
		system("touch /etc/kvm/oled_exist");
	} else {
		printf("oled not exist\r\n");
		unlink("/etc/kvm/oled_exist");
	}

	if(kvm_sys_state.sys_thread_running == 0){
		kvm_sys_state.sys_thread_running = 1;
		if (0 != pthread_create(&sys_state_thread, NULL, thread_sys_handle, NULL)) {
			kvm_sys_state.sys_thread_running = 0;
			printf("[kvms]create thread failed!\r\n");
		}
	}
	if(OLED_state == 1 && kvm_sys_state.oled_thread_running == 0){
		kvm_sys_state.oled_thread_running = 1;
		if (0 != pthread_create(&display_thread, NULL, thread_oled_handle, NULL)) {
			kvm_sys_state.oled_thread_running = 0;
			printf("[kvms]create thread failed!\r\n");
		} else {
			display_thread_started = true;
		}
	}
	if(kvm_sys_state.key_thread_running == 0){
		kvm_sys_state.key_thread_running = 1;
		if (0 != pthread_create(&key_thread, NULL, thread_key_handle, NULL)) {
			kvm_sys_state.key_thread_running = 0;
			printf("[kvms]create thread failed!\r\n");
		}
	}

	// while(!app::need_exit()){
	while(kvm_sys_state.sys_thread_running){
		kvms::sleep_ms(1000);
	}
	kvm_sys_state.sys_thread_running = 0;
	kvm_sys_state.oled_thread_running = 0;
	kvm_sys_state.key_thread_running = 0;
	// No OLED (or a failed pthread_create) means there is no display to join.
	if (display_thread_started) pthread_join(display_thread, nullptr);
}

