#ifndef OLED_UI_H_
#define OLED_UI_H_
#include "config.h"
#include "oled_ctrl.h"

void kvm_main_ui_disp(uint8_t first_disp, uint8_t subpage_changed);
void kvm_wifi_config_ui_disp(uint8_t first_disp, uint8_t subpage_changed);
void oled_auto_sleep_time_update(void);
void oled_auto_sleep(void);
void kvm_show_UE(void);

// A non-persistent, bounded wakeup used only when the user has disabled the
// panel and a usable IPv4 address is newly acquired. The system-state worker
// is the producer and the OLED worker is the consumer, so this API owns the
// synchronization instead of exposing its state structure.
void oled_ip_window_observe(const char *eth_addr, const char *wifi_addr);
bool OLED_IPWindowActive(void);

#endif // OLED_UI_H_
