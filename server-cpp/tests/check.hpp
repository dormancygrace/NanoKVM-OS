#pragma once

// CHECK for the security-boundary tests: reports every failed condition (not only the first) and is not disabled by
// NDEBUG; finish() gives the exit status.

#include <cstdio>
#include <string>

inline int failures = 0;

#define CHECK(cond, ...)                                                                                        \
	do {                                                                                                        \
		if (!(cond)) {                                                                                          \
			std::fprintf(stderr, "%s:%d: CHECK(%s) failed %s\n", __FILE__, __LINE__, #cond,                  \
			             (std::string() __VA_OPT__(+std::string(__VA_ARGS__))).c_str());                      \
			++failures;                                                                                         \
		}                                                                                                       \
	} while (0)

inline int finish(const char *name)
{
	std::printf("%s: %s\n", name, failures ? "FAILED" : "ok");
	return failures ? 1 : 0;
}
