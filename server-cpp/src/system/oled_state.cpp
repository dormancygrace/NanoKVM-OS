#include "system/oled_state.hpp"

#include "common/log.hpp"
#include "config/config.hpp"

#include <string>
#include <sys/stat.h>

namespace nkos::system::oled {

namespace {

int published_viewers = -1;

bool write_value(const char *path, int value)
{
	::mkdir("/run/nanokvm", 0755);
	if (!config::atomic_write(path, std::to_string(value) + "\n", 0644)) {
		log::debug("OLED state: cannot write {}", path);
		return false;
	}
	return true;
}

} // namespace

void publish_atx(bool enabled) { write_value("/run/nanokvm/atx", enabled ? 1 : 0); }

void publish_viewers(int count)
{
	if (count == published_viewers) return;
	if (write_value("/run/nanokvm/viewers", count)) published_viewers = count;
}

} // namespace nkos::system::oled
