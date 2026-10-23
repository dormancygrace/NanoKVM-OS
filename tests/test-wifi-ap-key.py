#!/usr/bin/env python3
"""The Wi-Fi provisioning key from kvm_system: getrandom, the symbol set, the
private file it is written to, and the re-look for a late wlan0."""
from pathlib import Path
import subprocess, tempfile

repo = Path(__file__).resolve().parents[1]
main = repo / 'support/sg2002/kvm_system/main'
ctrl = (main / 'lib/system_ctrl/system_ctrl.cpp').read_text()
state = (main / 'lib/system_state/system_state.cpp').read_text()
key = ctrl[ctrl.index('static const char wifi_ap_key_symbols'):ctrl.index('// The first line a command prints')]
presence = state[state.index('bool wifi_presence_due'):state.index('void kvm_update_wifi_state')]

# The provisioning path: no time-seeded rand(), no shell echo of the key, a
# fresh key only when the user starts provisioning, a bounded join wait.
assert 'srand(' not in ctrl and 'rand()' not in ctrl
assert 'echo %s > /kvmapp/kvm/ap.pass' not in ctrl
assert ctrl.count('\twifi_ap_new_key = true;') == 1
start = ctrl[ctrl.index('void kvm_start_wifi_config_process'):ctrl.index('void kvm_wifi_web_config_process')]
assert 'wifi_ap_new_key = true;' in start
assert 'WIFI_JOIN_TIMEOUT' in ctrl[ctrl.index('case 3:'):]
assert 'kvms::sleep_ms(WIFI_CONNECTION_DELAY)' not in ctrl[ctrl.index('void kvm_wifi_config_process'):]
# The page-0 connect never leaves provisioning half started: a later button
# press must begin with a new key.
web = ctrl[ctrl.index('void kvm_wifi_web_config_process'):ctrl.index('void kvm_wifi_config_process')]
assert 'wifi_config_process = -1;' in web and 'wifi_config_process = 0;' not in web
# A missing wlan0 is looked for again.
update = state[state.index('void kvm_update_wifi_state'):]
assert 'if(kvm_sys_state.wifi_state == -2) return;' not in update
assert 'wifi_presence_due(' in update

source = r'''
#include <assert.h>
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/random.h>
#include <sys/stat.h>
#include <unistd.h>
#define WIFI_PRESENCE_INTERVAL 5000
''' + key + presence + r'''
int main(int argc, char **argv) {
  assert(argc == 2);
  static int seen[256];
  char first[9] = {0}, k[9];
  for (int i = 0; i < 20000; i++) {
    assert(gen_wifi_ap_key(k, sizeof(k)));
    assert(strlen(k) == 8);
    for (const char *c = k; *c; c++) {
      assert(strchr(wifi_ap_key_symbols, *c));
      seen[(unsigned char)*c]++;
    }
    if (i == 0) memcpy(first, k, sizeof(k));
    else assert(strcmp(first, k) != 0 || i > 100000);
  }
  // 160000 symbols over 31: about 5161 each; a biased draw shows far off.
  for (const char *c = wifi_ap_key_symbols; *c; c++)
    assert(seen[(unsigned char)*c] > 4700 && seen[(unsigned char)*c] < 5650);
  for (const char *c = "01ilo"; *c; c++) assert(seen[(unsigned char)*c] == 0);
  assert(!gen_wifi_ap_key(k, 0));

  // A file an older release left 0644 is tightened; the content is replaced.
  FILE *fp = fopen(argv[1], "w"); assert(fp); fputs("old old old old\n", fp); fclose(fp);
  chmod(argv[1], 0644);
  assert(write_private_file(argv[1], "abcdefgh"));
  struct stat st; assert(stat(argv[1], &st) == 0); assert((st.st_mode & 0777) == 0600);
  char buf[32] = {0}; fp = fopen(argv[1], "r"); assert(fp);
  assert(fread(buf, 1, sizeof(buf) - 1, fp) == 9); fclose(fp);
  assert(strcmp(buf, "abcdefgh\n") == 0);

  uint64_t last = 0;
  assert(wifi_presence_due(last, 1000));
  assert(!wifi_presence_due(last, 5999));
  assert(wifi_presence_due(last, 6000));
  puts("AP key symbols and spread, private file, wlan0 re-look PASS");
}
'''
with tempfile.TemporaryDirectory() as directory:
    d = Path(directory)
    (d / 'test.cpp').write_text(source)
    subprocess.run(['g++', '-std=c++17', '-O2', '-Wall', '-Werror', str(d / 'test.cpp'), '-o', str(d / 'test')], check=True)
    subprocess.run([str(d / 'test'), str(d / 'ap.pass')], check=True)
