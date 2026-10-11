#pragma once
// Adapted from yuzi-co/IronKVM 54c03d47: bounded gateway ICMP without fork.
#include <stddef.h>
#include <netinet/in.h>

int parse_gateway(const char *text, size_t length, struct in_addr *out);
// 1: reply, 0: no reply, -1: raw socket/interface unavailable (use ping).
int icmp_probe(const char *interface_name, struct in_addr destination, int timeout_ms);
