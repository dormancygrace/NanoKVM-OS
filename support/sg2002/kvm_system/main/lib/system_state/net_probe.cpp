#include "net_probe.h"

#include <arpa/inet.h>
#include <atomic>
#include <errno.h>
#include <netinet/ip.h>
#include <netinet/ip_icmp.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

static uint32_t probe_now_ms(void)
{
	struct timespec now;
	clock_gettime(CLOCK_MONOTONIC, &now);
	return (uint32_t)((uint64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000);
}

int parse_gateway(const char *s, size_t len, struct in_addr *out)
{
	char buf[INET_ADDRSTRLEN];
	size_t n = 0;
	while (n < len && n < sizeof(buf) - 1 && ((s[n] >= '0' && s[n] <= '9') || s[n] == '.')) {
		buf[n] = s[n];
		n++;
	}
	// Something follows that is neither the end nor a separator, such as a
	// host name in /etc/kvm/gateway: not an address this can read.
	if (n < len && s[n] != 0 && s[n] != ' ' && s[n] != '\n' && s[n] != '\r' && s[n] != '\t') return 0;
	buf[n] = 0;
	if (n == 0) return 0;
	return inet_pton(AF_INET, buf, out) == 1;
}

static uint16_t icmp_checksum(const void *data, size_t len)
{
	const uint8_t *p = (const uint8_t *)data;
	uint32_t sum = 0;
	while (len > 1) {
		sum += (uint32_t)((p[0] << 8) | p[1]);
		p += 2;
		len -= 2;
	}
	if (len) sum += (uint32_t)(p[0] << 8);
	while (sum >> 16) sum = (sum & 0xffff) + (sum >> 16);
	return htons((uint16_t)~sum);
}

int icmp_probe(const char *ifname, struct in_addr dst, int timeout_ms)
{
	static std::atomic<uint16_t> sequence{0};
	if (timeout_ms <= 0) return 0;

	int sock = socket(AF_INET, SOCK_RAW | SOCK_CLOEXEC | SOCK_NONBLOCK, IPPROTO_ICMP);
	if (sock < 0) return -1;
	// "ping -I eth0" binds to the device, so the probe leaves by that
	// interface even when the routing table would send it by another.
	if (setsockopt(sock, SOL_SOCKET, SO_BINDTODEVICE, ifname, strlen(ifname) + 1) < 0) {
		close(sock);
		return -1;
	}

	// A raw ICMP socket sees every echo reply the host receives, including
	// those to a ping someone runs by hand, so the id and sequence tell ours
	// apart.
	uint16_t id = (uint16_t)getpid();
	const uint16_t seq = ++sequence;

	alignas(struct icmphdr) uint8_t packet[sizeof(struct icmphdr) + 16];
	memset(packet, 0, sizeof(packet));
	struct icmphdr *req = (struct icmphdr *)packet;
	req->type = ICMP_ECHO;
	req->code = 0;
	req->un.echo.id = htons(id);
	req->un.echo.sequence = htons(seq);
	memcpy(packet + sizeof(struct icmphdr), "kvm_system probe", 16);
	req->checksum = icmp_checksum(packet, sizeof(packet));

	struct sockaddr_in to;
	memset(&to, 0, sizeof(to));
	to.sin_family = AF_INET;
	to.sin_addr = dst;
	if (sendto(sock, packet, sizeof(packet), 0, (struct sockaddr *)&to, sizeof(to)) < 0) {
		// No route, link down, and the like. ping reports these as a failure.
		close(sock);
		return 0;
	}

	int result = 0;
	uint32_t start = probe_now_ms();
	for (;;) {
		uint32_t elapsed = probe_now_ms() - start;
		if (elapsed >= (uint32_t)timeout_ms) break;
		struct pollfd pfd;
		pfd.fd = sock;
		pfd.events = POLLIN;
		pfd.revents = 0;
		int ready = poll(&pfd, 1, timeout_ms - (int)elapsed);
		if (ready < 0) {
			if (errno == EINTR) continue;
			break;
		}
		if (ready == 0) break;

		alignas(struct iphdr) uint8_t reply[256];
		struct sockaddr_in from;
		socklen_t from_len = sizeof(from);
		ssize_t n = recvfrom(sock, reply, sizeof(reply), 0, (struct sockaddr *)&from, &from_len);
		if (n < 0) {
			if (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK) continue;
			break;
		}
		// A raw socket hands back the IP header in front of the ICMP one.
		if ((size_t)n < sizeof(struct iphdr)) continue;
		size_t ihl = (size_t)(((const struct iphdr *)reply)->ihl) * 4;
		if (ihl < sizeof(struct iphdr) || (size_t)n < ihl + sizeof(struct icmphdr)) continue;
		const struct icmphdr *rep = (const struct icmphdr *)(reply + ihl);
		if (rep->type != ICMP_ECHOREPLY || rep->code != 0) continue;
		if (ntohs(rep->un.echo.id) != id || ntohs(rep->un.echo.sequence) != seq) continue;
		if (from.sin_addr.s_addr != dst.s_addr) continue;
		result = 1;
		break;
	}
	close(sock);
	return result;
}
