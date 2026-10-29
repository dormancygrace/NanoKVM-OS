#!/usr/bin/env python3
"""Exercise production OLED window state with a deterministic monotonic clock."""
from pathlib import Path
import subprocess, tempfile
repo=Path(__file__).resolve().parents[1]
s=(repo/'support/sg2002/kvm_system/main/lib/oled_ui/oled_ui.cpp').read_text()
state=s[s.index('constexpr uint64_t OLED_IP_WINDOW_MS'):s.index('bool ip_window_rendered')]
functions=s[s.index('bool OLED_IPWindowActive(void)'):s.index('void kvm_init_cube_ui(void)')]
# Keep the substituted state path longer than 64 bytes, including in CI.
with tempfile.TemporaryDirectory(prefix="nanokvm-oled-ip-window-state-with-a-long-temporary-path-") as directory:
    d=Path(directory)
    source=r"""
#include <pthread.h>
#include <sys/stat.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <assert.h>
#include <unistd.h>
static uint64_t now_ms=1;
static bool disabled=true;
static uint64_t oled_monotonic_ms(){return now_ms;}
static bool OLED_IsDisabled(){return disabled;}
"""+state.replace('/etc/kvm/oled_sleep',str(d/'setting')).replace('/run/kvm_system-oled-ip-window',str(d/'window'))+functions+r"""
// A new kvm_system process of the same boot: memory is gone, the file stays.
static void restart(){oled_ip_window=oled_ip_window_t{};}
static void write_window(const char *text){FILE *fp=fopen(OLED_IP_WINDOW_STATE,"w");assert(fp);fputs(text,fp);fclose(fp);}
static void restarts(){
 unlink(OLED_IP_WINDOW_STATE);restart();now_ms=1;
 // A boot (no file) opens a window for the first address.
 oled_ip_window_observe("192.168.1.2","");assert(OLED_IPWindowActive());
 const auto deadline=oled_ip_window.deadline_ms;
 // A restart inside the window keeps it, without extending it.
 now_ms+=60000;restart();
 oled_ip_window_observe("192.168.1.2","");assert(OLED_IPWindowActive()&&oled_ip_window.deadline_ms==deadline);
 // A restart after it, with the same addresses, does not open another.
 now_ms=deadline+1;restart();
 oled_ip_window_observe("192.168.1.2","");assert(!OLED_IPWindowActive());
 restart();oled_ip_window_observe("192.168.1.2","");assert(!OLED_IPWindowActive());
 // A new address while kvm_system was not running does.
 restart();oled_ip_window_observe("192.168.1.2","10.0.0.1");assert(OLED_IPWindowActive());
 // So does an address that went away and came back.
 now_ms=oled_ip_window.deadline_ms;oled_ip_window_observe("","10.0.0.1");
 restart();oled_ip_window_observe("192.168.1.2","10.0.0.1");assert(OLED_IPWindowActive());
 // An unreadable file, or a deadline no window could have, counts as none.
 now_ms+=OLED_IP_WINDOW_MS;write_window("garbage");restart();
 oled_ip_window_observe("192.168.1.2","");assert(OLED_IPWindowActive());
 char text[64];snprintf(text,sizeof(text),"192.168.1.2 - %llu\n",(unsigned long long)(now_ms+2*OLED_IP_WINDOW_MS));
 write_window(text);restart();
 oled_ip_window_observe("192.168.1.2","");assert(!OLED_IPWindowActive());
 unlink(OLED_IP_WINDOW_STATE);restart();now_ms=1;
}
int main(int argc, char **argv){
 assert(argc==2);
 restarts();
 FILE *fp=fopen(argv[1],"w");assert(fp);fputs("-1",fp);fclose(fp);
 oled_ip_window_observe("","");assert(!OLED_IPWindowActive());
 oled_ip_window_observe("192.168.1.2","");assert(OLED_IPWindowActive());
 const auto deadline=oled_ip_window.deadline_ms;
 now_ms+=200000;
 oled_ip_window_observe("192.168.1.2","10.0.0.1");assert(oled_ip_window.deadline_ms==deadline);
 oled_ip_window_observe("","");
 oled_ip_window_observe("192.168.1.3","10.0.0.2");assert(oled_ip_window.deadline_ms==deadline);
 now_ms=deadline;assert(!OLED_IPWindowActive());
 oled_ip_window_observe("192.168.1.3","10.0.0.2");assert(!OLED_IPWindowActive());
 oled_ip_window_observe("192.168.1.4","10.0.0.2");assert(OLED_IPWindowActive());
 // Replacing the setting file is an explicit manual choice, even for -1.
 assert(unlink(argv[1])==0);fp=fopen(argv[1],"w");assert(fp);fputs("-1\n",fp);fclose(fp);
 oled_ip_window_observe("192.168.1.4","10.0.0.2");assert(!OLED_IPWindowActive());
 oled_ip_window_observe("192.168.1.4","10.0.0.2");assert(!OLED_IPWindowActive());
 disabled=false;oled_ip_window_observe("192.168.1.5","");assert(!OLED_IPWindowActive());
 char text[24]={};format_window_line(text,sizeof(text),'W',"255.255.255.255",true);
 assert(strcmp(text,"W255.255.255.255")==0 && strlen(text)==16);
 puts("OLED window expiry, no-extension, restarts, manual override and IPv4 formatting PASS");
}
"""
    (d/'test.cpp').write_text(source)
    subprocess.run(['g++','-std=c++17','-O2','-pthread',str(d/'test.cpp'),'-o',str(d/'test')],check=True)
    subprocess.run([str(d/'test'),str(d/'setting')],check=True)
