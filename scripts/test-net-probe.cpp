#include "net_probe.h"
#include <arpa/inet.h>
#include <cassert>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <unistd.h>
int main() {
    in_addr addr{};
    assert(parse_gateway("127.0.0.1\n", 10, &addr));
    assert(!parse_gateway("127.0.0.1x", 10, &addr));
    assert(!parse_gateway("300.0.0.1", 9, &addr));
    assert(!parse_gateway("gateway.lan", 11, &addr));
    assert(parse_gateway("192.168.100.100", 15, &addr));
    assert(inet_pton(AF_INET, "127.0.0.1", &addr) == 1);
    assert(icmp_probe("nosuchif0", addr, 100) == -1);
    const int result = icmp_probe("lo", addr, 500);
    if (getuid() == 0) assert(result == 1);
    else if (result == -1) { puts("SKIP: raw socket unavailable; parsing/fallback passed"); return 0; }
    else assert(result == 1);
    assert(inet_pton(AF_INET, "192.0.2.1", &addr) == 1);
    const auto start = std::chrono::steady_clock::now();
    assert(icmp_probe("lo", addr, 100) == 0);
    const auto took = std::chrono::steady_clock::now() - start;
    assert(took < std::chrono::seconds(1));
    puts("PASS: ICMP reply, interface fallback and bounded no-reply wait");
}
