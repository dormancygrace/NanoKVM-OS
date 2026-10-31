#include "system_ctrl.h"
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <sys/random.h>
#include <sys/stat.h>
#include <unistd.h>


extern kvm_sys_state_t kvm_sys_state;
extern kvm_oled_state_t kvm_oled_state;

void gen_hostapd_conf(char* ap_ssid)
{
	char file_data[300] = {0};
	FILE * fp;
	sprintf(file_data, "ctrl_interface=/var/run/hostapd\nctrl_interface_group=0\nssid=NanoKVM\nhw_mode=g\nchannel=1\nbeacon_int=100\ndtim_period=2\nmax_num_sta=255\nrts_threshold=-1\nfragm_threshold=-1\nmacaddr_acl=0\nauth_algs=3\nwpa=2\nwpa_passphrase=%s\nieee80211n=1\n", ap_ssid);
	fp = fopen("/etc/hostapd.conf", "w");
	fwrite(file_data, sizeof(file_data) , 1, fp );
	fclose(fp);
}

void gen_udhcpd_conf()
{
	char file_data[300] = {0};
	FILE * fp;
	sprintf(file_data, "start 10.10.10.100\nend 10.10.10.200\ninterface wlan0\npidfile /var/run/udhcpd.wlan0.pid\nlease_file /var/lib/misc/udhcpd.wlan0.leases\noption subnet 255.255.255.0\noption lease 864000\n");
	fp = fopen("/etc/udhcpd.wlan0.conf", "w");
	fwrite(file_data, sizeof(file_data) , 1, fp );
	fclose(fp);
}

void gen_dnsmasq_conf()
{
	char file_data[300] = {0};
	FILE * fp;
	sprintf(file_data, "bind-interfaces\ninterface=wlan0\ndhcp-range=10.10.10.2,10.10.10.254\naddress=/#/10.0.0.1\n");
	fp = fopen("/etc/udhcpd.wlan0.conf", "w");
	fwrite(file_data, sizeof(file_data) , 1, fp );
	fclose(fp);
}

// The AP key is the access point's WPA2 passphrase and the X-AP-Key of the
// server's provisioning routes. 8 characters fit the OLED's 64-pixel line;
// from 31 symbols that cannot be confused (no 0/o, 1/l/i) they carry about
// 40 bits, from the kernel's generator. A failure leaves out empty.
static const char wifi_ap_key_symbols[] = "23456789abcdefghjkmnpqrstuvwxyz";

bool gen_wifi_ap_key(char *out, size_t size)
{
	const size_t symbols = sizeof(wifi_ap_key_symbols) - 1;
	// Bytes at or above the largest multiple of 31 are drawn again, so every
	// symbol is equally likely.
	const unsigned limit = 256 - 256 % symbols;
	size_t n = 0;
	if (size == 0) return false;
	out[0] = '\0';
	while (n + 1 < size) {
		unsigned char bytes[16];
		ssize_t got = getrandom(bytes, sizeof(bytes), 0);
		if (got < 0) {
			if (errno == EINTR) continue;
			out[0] = '\0';
			return false;
		}
		for (ssize_t i = 0; i < got && n + 1 < size; i++) {
			if (bytes[i] < limit) out[n++] = wifi_ap_key_symbols[bytes[i] % symbols];
		}
	}
	out[n] = '\0';
	return true;
}

// Writes text and a newline to path, readable by root only.
bool write_private_file(const char *path, const char *text)
{
	int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return false;
	// open keeps the mode of a file an older release created 0644.
	bool ok = fchmod(fd, 0600) == 0;
	const size_t length = strlen(text);
	ok = ok && write(fd, text, length) == (ssize_t)length && write(fd, "\n", 1) == 1;
	return close(fd) == 0 && ok;
}

// The first line a command prints, "" when it fails or prints nothing.
static void first_output_line(const char *command, char *line, size_t size)
{
	line[0] = '\0';
	FILE *fp = popen(command, "r");
	if (fp == NULL) return;
	if (fgets(line, (int)size, fp) == NULL) line[0] = '\0';
	pclose(fp);
}

uint8_t sta_connect_ap(void)
{
	char line[16];
	first_output_line("hostapd_cli all sta | grep aid", line, sizeof(line));
	return line[0] == 'a';
}

uint8_t ssid_pass_ok(void)
{
	if(access("/kvmapp/kvm/wifi_try_connect", F_OK) == 0) return 1;
	else return 0;
}

uint8_t wifi_connected(void)
{
	char line[32];
	first_output_line("wpa_cli -i wlan0 status | grep \"wpa_state\"", line, sizeof(line));
	return strncmp(line, "wpa_state=COMPLETED", 19) == 0;
}

// A new key for each provisioning the user starts; a join that fails brings
// the access point back with the same key, so the QR code and the URL the
// user holds stay valid.
static bool wifi_ap_new_key = true;
// When the join being tried started; it gets WIFI_JOIN_TIMEOUT to complete.
static uint64_t wifi_join_started_ms = 0;

void kvm_start_wifi_config_process(void)
{
	if(kvm_sys_state.wifi_config_process == -1){
		// printf("[kvmw]开始配置wifi\n");
		kvm_sys_state.wifi_config_process = 0;
		kvm_sys_state.sub_page = 0;
		wifi_ap_new_key = true;
	}
}

// The admin's legacy Wi-Fi connect on the main page. It never enters
// provisioning: whatever the join does, the state stays -1, so the next
// button press starts provisioning (and a new key) from the beginning.
void kvm_wifi_web_config_process()
{
	if(ssid_pass_ok()){
		system("rm /kvmapp/kvm/wifi_try_connect");
		system("/etc/init.d/S30wifi restart");
		kvm_sys_state.wifi_config_process = -1;
	}
}

void kvm_wifi_config_process()
{	
	switch(kvm_sys_state.wifi_config_process){
		case -1:
			// printf("[kvms]Not in the process of configuring WiFi\n");
			break;
		case 0: // 启动wifi
			if (wifi_ap_new_key || kvm_sys_state.wifi_ap_pass[0] == '\0') {
				if (!gen_wifi_ap_key(kvm_sys_state.wifi_ap_pass, sizeof(kvm_sys_state.wifi_ap_pass))) {
					// No key, no access point: try again on the next round.
					break;
				}
				wifi_ap_new_key = false;
			}
			if (!write_private_file("/kvmapp/kvm/ap.ssid", "NanoKVM") ||
				!write_private_file("/kvmapp/kvm/ap.pass", kvm_sys_state.wifi_ap_pass)) {
				break;
			}
			system("/etc/init.d/S30wifi ap");

			kvm_sys_state.wifi_config_process = 1;
			kvm_sys_state.sub_page = 1;
			break;
		case 1: // 等待设备连接
			if(sta_connect_ap()){
				kvm_sys_state.wifi_config_process = 2;
				kvm_sys_state.sub_page = 3;
			}
			break;
		case 2: // 等待输入帐号密码
			if(ssid_pass_ok()){
				system("rm /kvmapp/kvm/wifi_try_connect");
				system("/etc/init.d/S30wifi restart");
				kvm_sys_state.wifi_config_process = 3;
				kvm_sys_state.sub_page = 5;
				wifi_join_started_ms = kvms::ticks_ms();
			}
			break;
		case 3: // 尝试连接wifi
			// S30wifi starts wpa_supplicant in the background; SAE and DFS
			// channels often take longer than a few seconds. Check on every
			// round until the join completes or WIFI_JOIN_TIMEOUT passes.
			if(wifi_connected()){
				// 连上了
				kvm_sys_state.wifi_config_process = -1;
				kvm_sys_state.page = 0;
				kvm_sys_state.sub_page = 0;
			} else if (kvms::ticks_ms() - wifi_join_started_ms >= WIFI_JOIN_TIMEOUT) {
				// 没连上
				kvm_sys_state.wifi_config_process = 0;
				kvm_sys_state.sub_page = 0;
			}
			break;
	}
}

uint8_t kvm_reset_password(void)
{
	FILE *fp = popen("bash", "w");
    if (fp == NULL) {
        perror("popen");
        return 0;
    }

    fputs("passwd root\n", fp);
	kvms::sleep_ms(10);
    fputs("root\n", fp);
	kvms::sleep_ms(10);
    fputs("root\n", fp);
	kvms::sleep_ms(10);
    fputs("rm /etc/kvm/pwd\n", fp);
	kvms::sleep_ms(10);
    fputs("sync\n", fp);
	kvms::sleep_ms(10);
    fputs("exit\n", fp); // 退出 bash

    if (pclose(fp) == -1) {
        perror("pclose");
        return 1;
    }
    return 0;
}