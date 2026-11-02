#include "oled_ui.h"
#include "oled_layout.h"
#include <sys/sysinfo.h>
#include <time.h>
#include <algorithm>
#include <string>

static uint64_t oled_monotonic_ms();

namespace {

constexpr uint64_t OLED_IP_WINDOW_MS = 10U * 60U * 1000U;
// The addresses last seen and the window's deadline, for the next kvm_system
// of the same boot: a restart (a package upgrade, nanokvm-app restart) is not
// a new address. /run starts empty at boot, so a boot still opens a window.
constexpr char OLED_IP_WINDOW_STATE[] = "/run/kvm_system-oled-ip-window";

struct oled_ip_window_t {
    pthread_mutex_t mutex = PTHREAD_MUTEX_INITIALIZER;
    char eth_addr[16] = {};
    char wifi_addr[16] = {};
    uint64_t deadline_ms = 0;
    bool restored = false;
    bool settings_stamp_known = false;
    struct stat settings_stamp = {};
};

oled_ip_window_t oled_ip_window;

// Called with the mutex held. One line: eth wifi deadline, "-" for no address.
void restore_ip_window(uint64_t now)
{
    FILE *fp = fopen(OLED_IP_WINDOW_STATE, "r");
    if (fp == NULL) return;
    char eth[16] = {};
    char wifi[16] = {};
    unsigned long long deadline = 0;
    const bool parsed = fscanf(fp, "%15s %15s %llu", eth, wifi, &deadline) == 3;
    fclose(fp);
    if (!parsed) return;
    snprintf(oled_ip_window.eth_addr, sizeof(oled_ip_window.eth_addr), "%s", strcmp(eth, "-") ? eth : "");
    snprintf(oled_ip_window.wifi_addr, sizeof(oled_ip_window.wifi_addr), "%s", strcmp(wifi, "-") ? wifi : "");
    // CLOCK_MONOTONIC runs on across process restarts within a boot.
    if (deadline > now && deadline - now <= OLED_IP_WINDOW_MS) oled_ip_window.deadline_ms = deadline;
}

// Called with the mutex held, only when something changed.
void save_ip_window()
{
    char temporary[sizeof(OLED_IP_WINDOW_STATE) + sizeof(".tmp") - 1];
    snprintf(temporary, sizeof(temporary), "%s.tmp", OLED_IP_WINDOW_STATE);
    FILE *fp = fopen(temporary, "w");
    if (fp == NULL) return;
    const bool written = fprintf(fp, "%s %s %llu\n",
        oled_ip_window.eth_addr[0] ? oled_ip_window.eth_addr : "-",
        oled_ip_window.wifi_addr[0] ? oled_ip_window.wifi_addr : "-",
        (unsigned long long)oled_ip_window.deadline_ms) > 0;
    if (fclose(fp) == 0 && written) rename(temporary, OLED_IP_WINDOW_STATE);
    else unlink(temporary);
}

bool same_settings_stamp(const struct stat& left, const struct stat& right)
{
    return left.st_dev == right.st_dev && left.st_ino == right.st_ino &&
        left.st_size == right.st_size && left.st_mtim.tv_sec == right.st_mtim.tv_sec &&
        left.st_mtim.tv_nsec == right.st_mtim.tv_nsec;
}

// A later settings write is explicit user authority, even when it writes -1
// again. The first observation merely establishes a baseline at boot.
bool oled_settings_changed()
{
    struct stat current = {};
    const bool present = stat("/etc/kvm/oled_sleep", &current) == 0;
    pthread_mutex_lock(&oled_ip_window.mutex);
    const bool changed = oled_ip_window.settings_stamp_known &&
        (!present || !same_settings_stamp(oled_ip_window.settings_stamp, current));
    if (present) oled_ip_window.settings_stamp = current;
    oled_ip_window.settings_stamp_known = true;
    pthread_mutex_unlock(&oled_ip_window.mutex);
    return changed;
}

bool copy_ip_window_snapshot(char *eth_addr, char *wifi_addr, size_t size)
{
    if (eth_addr == NULL || wifi_addr == NULL || size < 16U) return false;
    const uint64_t now = oled_monotonic_ms();
    pthread_mutex_lock(&oled_ip_window.mutex);
    const bool active = oled_ip_window.deadline_ms > now;
    if (active) {
        memcpy(eth_addr, oled_ip_window.eth_addr, sizeof(oled_ip_window.eth_addr));
        memcpy(wifi_addr, oled_ip_window.wifi_addr, sizeof(oled_ip_window.wifi_addr));
    }
    pthread_mutex_unlock(&oled_ip_window.mutex);
    return active;
}

void format_window_line(char *output, size_t size, char prefix, const char *address, bool narrow)
{
    const char *shown = address != NULL && address[0] != 0 ? address : "--";
    if (narrow) {
        // Sixteen 4-pixel characters fit in the PCIe panel. A maximal IPv4
        // address therefore omits only the separator, never an address digit.
        if (strlen(shown) >= 15U) snprintf(output, size, "%c%.15s", prefix, shown);
        else snprintf(output, size, "%c:%.14s", prefix, shown);
    } else {
        snprintf(output, size, "%s: %.15s", prefix == 'E' ? "ETH" : "WiFi", shown);
    }
}

bool ip_window_rendered = false;

void oled_ip_window_status(bool force)
{
    char eth_addr[16] = {};
    char wifi_addr[16] = {};
    if (!copy_ip_window_snapshot(eth_addr, wifi_addr, sizeof(eth_addr))) return;

    static uint64_t moved = 0;
    static unsigned step = 0;
    static char shown[2][24] = {};
    const bool narrow = kvm_hw_ver == 2;
    const unsigned width = narrow ? 64U : 128U;
    const unsigned pages = narrow ? 4U : 8U;
    const unsigned font = narrow ? 4U : 8U;
    char lines[2][24] = {};
    format_window_line(lines[0], sizeof(lines[0]), 'E', eth_addr, narrow);
    format_window_line(lines[1], sizeof(lines[1]), 'W', wifi_addr, narrow);

    const unsigned line_width = std::max(strlen(lines[0]), strlen(lines[1])) * (narrow ? 4U : 6U);
    const unsigned block_width = std::min(width, line_width);
    const uint64_t now = oled_monotonic_ms();
    if (!moved || now - moved >= 60000U) {
        moved = now;
        ++step;
        force = true;
    }
    const unsigned x = (step * 13U) % (width - block_width + 1U);
    const unsigned page = step % (pages - 2U + 1U);
    force = force || strcmp(lines[0], shown[0]) != 0 || strcmp(lines[1], shown[1]) != 0;
    if (force) OLED_Clear();
    for (unsigned i = 0; i < 2U; ++i) {
        if (force || strcmp(lines[i], shown[i]) != 0) {
            OLED_ShowString(x, page + i, lines[i], font);
            strcpy(shown[i], lines[i]);
        }
    }
    ip_window_rendered = true;
}

} // namespace

static uint64_t oled_monotonic_ms() {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return uint64_t(ts.tv_sec) * 1000 + ts.tv_nsec / 1000000;
}


extern kvm_sys_state_t kvm_sys_state;
extern kvm_oled_state_t kvm_oled_state;
void OLED_Display_On(void);
void OLED_Display_Off(void);

bool OLED_IPWindowActive(void)
{
    const uint64_t now = oled_monotonic_ms();
    pthread_mutex_lock(&oled_ip_window.mutex);
    const bool active = oled_ip_window.deadline_ms > now;
    pthread_mutex_unlock(&oled_ip_window.mutex);
    return active;
}

void oled_ip_window_observe(const char *eth_addr, const char *wifi_addr)
{
    const bool settings_changed = oled_settings_changed();
    const bool persistently_disabled = OLED_IsDisabled();
    const uint64_t now = oled_monotonic_ms();
    pthread_mutex_lock(&oled_ip_window.mutex);
    if (!oled_ip_window.restored) {
        oled_ip_window.restored = true;
        restore_ip_window(now);
    }

    const char *next_eth = eth_addr == NULL ? "" : eth_addr;
    const char *next_wifi = wifi_addr == NULL ? "" : wifi_addr;
    const bool eth_changed = strcmp(next_eth, oled_ip_window.eth_addr) != 0;
    const bool wifi_changed = strcmp(next_wifi, oled_ip_window.wifi_addr) != 0;
    const bool eth_event = next_eth[0] != 0 && eth_changed;
    const bool wifi_event = next_wifi[0] != 0 && wifi_changed;
    const uint64_t previous_deadline = oled_ip_window.deadline_ms;
    snprintf(oled_ip_window.eth_addr, sizeof(oled_ip_window.eth_addr), "%s", next_eth);
    snprintf(oled_ip_window.wifi_addr, sizeof(oled_ip_window.wifi_addr), "%s", next_wifi);

    if (settings_changed || !persistently_disabled) {
        oled_ip_window.deadline_ms = 0;
    } else if ((eth_event || wifi_event) && oled_ip_window.deadline_ms <= now) {
        // Do not extend an existing window. This bounds active time under
        // DHCP renewals, address churn, or repeated carrier flaps.
        oled_ip_window.deadline_ms = now + OLED_IP_WINDOW_MS;
    }
    if (eth_changed || wifi_changed || oled_ip_window.deadline_ms != previous_deadline) save_ip_window();
    pthread_mutex_unlock(&oled_ip_window.mutex);
}

void qrcode_to_oled(QRCode *qr)
{
	char *p_oled_data;
	uint8_t begin_x = 2;
	uint8_t begin_y = 2;
	p_oled_data = (char *)malloc( 132 * sizeof(char));
	if(p_oled_data != NULL){
		// fill
		for(int i = 0; i < 132; i++){
			p_oled_data[i] = 0xFF;
		}
		// i | ; j -
		for(int i = 0; i < 29; i++){
			for(int j = 0; j < 29; j++){
				if(qrIsBlacke(qr, i, j)){
					// 敲黑点
					uint16_t data_index = ((i+begin_y)/8)*33+(j+begin_x);
					uint8_t mask = ~(0x01 << ((i+begin_y)%8));
					p_oled_data[data_index] &= mask;
				}
			}
		}
		OLED_Fill();
		OLED_ShowIMG(29, 0, p_oled_data, 33, 4);
		free(p_oled_data);
	}
}

int qrencode(char *string)
{
	// test: qrencode("WIFI:T:WPA2;S:NanoKVM;P:12345678;;");
	int errcode = QR_ERR_NONE;
	QRCode* p = qrInit(3, QR_EM_8BIT, 1, 4, &errcode);
	if (p == NULL) {
		printf("error\n");
		return -1;
	}
	qrAddData(p, (const qr_byte_t*)string, strlen(string));
	if (!qrFinalize(p)) {
		printf("finalize error\n");
		return -1;
	}

	qrcode_to_oled(p);
	if(string[0] == 'W'){
		OLED_ShowStringTurn(3, 1, "WiFi", 8);
		OLED_ShowStringTurn(3, 2, "AP:", 8);
	} else if(string[0] == 'h'){
		if(string[7] == '1'){
			OLED_ShowStringTurn(3, 1, "Web:", 8);
		} else if(string[8] == 'w'){
			OLED_ShowStringTurn(3, 1, "WiKi", 8);
		}
	}
	int size = 0;
	// width = height = qr_vertable[version] * mag + sep * mag * 2
	qr_byte_t * buffer = qrSymbolToBMP(p, 5, 5, &size);
	if (buffer == NULL) {
		printf("error %s", qrGetErrorInfo(p));
		return -1;
	}
	// output qrcode to file
	// ofstream f("/etc/kvm/wifi_config.bmp");
	// if (f.fail()) {
	// 	return -1;
	// }
	// f.write((const char *)buffer, size);
	// f.close();
	return 0;
}

// The main page's layout: one the web editor is previewing (written again
// while the preview runs, so a closed editor stops it), else the saved one,
// else the panel's built-in layout. A file is read again when it changes.
namespace {

const char OLED_LAYOUT_FILE[] = "/etc/kvm/oled_layout";
const char OLED_PREVIEW_FILE[] = "/run/nanokvm/oled_preview";
constexpr time_t OLED_PREVIEW_SECONDS = 15;

struct oled_layout_cache_t {
    std::string path;
    struct stat stamp = {};
    OledLayout layout;
    bool loaded = false;
};

oled_layout_cache_t oled_layout_cache;

bool read_layout_file(const char *path, std::string *text)
{
    FILE *fp = fopen(path, "r");
    if (fp == NULL) return false;
    char buffer[4096];
    size_t n;
    text->clear();
    while ((n = fread(buffer, 1, sizeof(buffer), fp)) > 0 && text->size() <= 65536) text->append(buffer, n);
    const bool ok = !ferror(fp) && text->size() <= 65536;
    fclose(fp);
    return ok;
}

const OledLayout &current_layout()
{
    const bool narrow = kvm_hw_ver == 2;
    struct stat stamp = {};
    const char *path = NULL;
    if (stat(OLED_PREVIEW_FILE, &stamp) == 0 && time(NULL) - stamp.st_mtime <= OLED_PREVIEW_SECONDS)
        path = OLED_PREVIEW_FILE;
    else if (stat(OLED_LAYOUT_FILE, &stamp) == 0)
        path = OLED_LAYOUT_FILE;
    oled_layout_cache_t &cache = oled_layout_cache;
    const std::string wanted = path ? path : "";
    if (cache.loaded && cache.path == wanted && (!path || same_settings_stamp(cache.stamp, stamp)))
        return cache.layout;
    cache.path = wanted;
    cache.stamp = stamp;
    cache.loaded = true;
    std::string text, error;
    if (path && read_layout_file(path, &text)) {
        if (oled_layout_parse(text.c_str(), &cache.layout, &error) && cache.layout.narrow == narrow) {
            fprintf(stderr, "OLED: layout %s\n", path);
            return cache.layout;
        }
        fprintf(stderr, "OLED: %s not used: %s\n", path, error.empty() ? "for the other panel" : error.c_str());
    }
    fprintf(stderr, "OLED: layout built in\n");
    oled_layout_parse(oled_layout_default(narrow), &cache.layout, NULL);
    return cache.layout;
}

std::string state_address(const uint8_t *address)
{
    const size_t length = strnlen((const char *)address, 16);
    return length ? std::string((const char *)address, length) : std::string("--");
}

void layout_values(OledValues *values)
{
    values->eth = kvm_sys_state.eth_state >= 1;
    values->wifi = kvm_sys_state.wifi_state == 1;
    values->hdmi = kvm_sys_state.hdmi_state == 1;
    values->hid = kvm_sys_state.usb_state == 1;
    values->stream = kvm_sys_state.now_fps > 0;
    values->eth_ip = state_address(kvm_sys_state.eth_addr);
    values->wifi_ip = state_address(kvm_sys_state.wifi_addr);
    values->ip = values->eth && kvm_sys_state.eth_addr[0] ? values->eth_ip
        : values->wifi && kvm_sys_state.wifi_addr[0] ? values->wifi_ip : "--";
    values->codec = kvm_sys_state.type == KVM_TYPE_MJPG ? "MJPG" : kvm_sys_state.type == KVM_TYPE_H264 ? "H264"
        : kvm_sys_state.type == KVM_TYPE_H265 ? "H265" : "VID";
    values->width = std::max<int>(kvm_sys_state.hdmi_width, 0);
    values->height = std::max<int>(kvm_sys_state.hdmi_height, 0);
    values->fps = std::max<int>(kvm_sys_state.now_fps, 0);
    char text[72] = {};
    values->hostname = gethostname(text, sizeof(text) - 1) == 0 ? text : "--";
    struct sysinfo info = {};
    const long minutes = sysinfo(&info) == 0 ? info.uptime / 60 : 0;
    if (minutes >= 24 * 60) snprintf(text, sizeof(text), "%ldd%02ldh", minutes / 1440, minutes / 60 % 24);
    else if (minutes >= 60) snprintf(text, sizeof(text), "%ldh%02ldm", minutes / 60, minutes % 60);
    else snprintf(text, sizeof(text), "%ldm", minutes);
    values->uptime = text;
    const time_t now = time(NULL);
    struct tm local = {};
    localtime_r(&now, &local);
    strftime(text, sizeof(text), "%H:%M", &local);
    values->time = text;
    strftime(text, sizeof(text), "%Y-%m-%d", &local);
    values->date = text;
}

// Drawn every pass; only the bytes that differ from the last frame go out.
// Once a minute, with the burn-in move, all of it goes out again, in case
// the display lost its RAM.
void oled_layout_status(bool force)
{
    static uint8_t shown[OLED_FRAME_BYTES];
    static bool shown_known = false;
    static uint64_t moved = 0;
    static unsigned step = 0;
    const uint64_t now = oled_monotonic_ms();
    if (!moved || now - moved >= 60000U) {
        moved = now;
        ++step;
        force = true;
    }
    const OledLayout &layout = current_layout();
    OledValues values;
    layout_values(&values);
    uint8_t frame[OLED_FRAME_BYTES];
    oled_layout_render(layout, values, step, frame);
    OLED_ShowFrame(frame, force || !shown_known ? NULL : shown, layout.narrow ? 64 : 128, layout.narrow ? 4 : 8);
    memcpy(shown, frame, sizeof(shown));
    shown_known = true;
}

} // namespace

void kvm_main_ui_disp(uint8_t first_disp, uint8_t subpage_changed)
{
    if (OLED_IPWindowActive()) {
        oled_ip_window_status(first_disp || subpage_changed || !ip_window_rendered);
        return;
    }
    const bool force = first_disp || subpage_changed || ip_window_rendered;
    ip_window_rendered = false;
    if (kvm_oled_state.oled_sleep_state || OLED_IsDisabled()) return;
    oled_layout_status(force);
}

void show_text_wifi_config(char *ap_ssid)
{
	OLED_Clear();
	OLED_ShowString(0, 0, "SSID:", 8);
	OLED_ShowString_AlignRight(63, 1, "NanoKVM", 8);
	OLED_ShowString(0, 2, "PASS:", 8);
	OLED_ShowString_AlignRight(63, 3, ap_ssid, 8);
}

void show_wifi_config_ip(void)
{
	OLED_Clear();
	get_ip_addr(WiFi_IP);
	OLED_ShowString(1, 0, "Config URL", 8);
	OLED_ShowString_AlignRight(63, 1, "----------------", 4);
	// OLED_ShowString_AlignRight(63, 2, (char*)kvm_sys_state.wifi_addr, 4);
	static char wifi_addr_with_path[30];
	static char wifi_addr_with_key[30];
	sprintf(wifi_addr_with_path, "%s/#/", kvm_sys_state.wifi_addr);
	sprintf(wifi_addr_with_key, "WIFI?P=%s", kvm_sys_state.wifi_ap_pass);
	OLED_ShowString_AlignRight(63, 2, wifi_addr_with_path, 4);
	OLED_ShowString_AlignRight(63, 3, wifi_addr_with_key, 4);
}

void show_wifi_config_QR(void)
{
	static char cmd[70];
	OLED_Clear();
	get_ip_addr(WiFi_IP);
	sprintf(cmd, "http://%s/#/WIFI?P=%s", kvm_sys_state.wifi_addr, kvm_sys_state.wifi_ap_pass);
	qrencode(cmd);
}

void show_wifi_starting(void)
{
	OLED_Clear();
	OLED_ShowString(0, 1, "WiFi AP is", 8);
	OLED_ShowString(0, 2, "Starting..", 8);
}

void show_wifi_connecting(void)
{
	OLED_Clear();
	OLED_ShowString(0, 1, "WiFi", 8);
	OLED_ShowString(0, 2, "Connect...", 8);
}

void kvm_wifi_config_ui_disp(uint8_t first_disp, uint8_t subpage_changed)
{
	static char cmd[70];
	// printf("[kvmd]kvm_wifi_config_ui_disp %d | %d\n", first_disp, subpage_changed);
	if(first_disp) kvm_start_wifi_config_process(); // 略有不妥,就这样吧
	if(first_disp || subpage_changed){
		switch(kvm_oled_state.sub_page){
			case 0: // wifi ap is starting
				show_wifi_starting();
				break;
			case 1: // QRcode
				printf("WIFI:T:WPA2;S:NanoKVM;P:%s;;\n", kvm_sys_state.wifi_ap_pass);
				sprintf(cmd, "WIFI:T:WPA2;S:NanoKVM;P:%s;;", kvm_sys_state.wifi_ap_pass);
				qrencode(cmd);
				break;
			case 2: // Textcode
				show_text_wifi_config(kvm_sys_state.wifi_ap_pass);
				break;
			case 3: // open IP with QR Code
				show_wifi_config_QR();
				break;
			case 4: // open IP
				show_wifi_config_ip();
				break;
			case 5: // Connecting...
				show_wifi_connecting();
				break;
		}
	}
	// switch(show_which_page())
}

void oled_auto_sleep_time_update(void)
{
	kvm_oled_state.oled_sleep_start = oled_monotonic_ms();
}

static void oled_set_sleep_state(uint8_t sleeping)
{
	if(kvm_oled_state.oled_sleep_state == sleeping){
		return;
	}

	if(sleeping){
		OLED_Clear();
		OLED_Display_Off();
	} else {
		OLED_Display_On();
	}
	kvm_oled_state.oled_sleep_state = sleeping;
}

void oled_auto_sleep(void)
{
    static int previous = -2;
    int duration = 0;
    FILE *fp = fopen("/etc/kvm/oled_sleep", "r");
    if (fp) {
        char text[16] = {};
        if (fgets(text, sizeof(text), fp)) duration = atoi(text);
        fclose(fp);
    }
    if (duration != previous) {
        previous = duration;
        oled_auto_sleep_time_update();
    }
    const bool ip_window_active = OLED_IPWindowActive();
    bool sleeping = duration == -1 && !ip_window_active;
    if (!sleeping && kvm_sys_state.page == 0 && duration >= OLED_SLEEP_DELAY_MIN) {
        sleeping = oled_monotonic_ms() - kvm_oled_state.oled_sleep_start >= uint64_t(duration) * 1000;
    }
    oled_set_sleep_state(sleeping);
    if (kvm_sys_state.page == 0) kvm_sys_state.sub_page = sleeping ? 1 : 0;
}

void kvm_show_UE(void)
{
	if(kvm_hw_ver != 2){
		OLED_ShowString(0, 0, "HDMI: UE", 16);
	} else {
		OLED_Revolve();
		OLED_ShowString(0, 0, "HDMI: UE", 16);
		// OLED_ShowString_AlignRight(AlignRightEND_P, 3, "  FPS", 4);
		// kvm_init_pcie_ui();
	}
}
