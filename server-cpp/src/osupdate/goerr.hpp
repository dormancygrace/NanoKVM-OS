#pragma once

// Error texts as the Go server writes them into API answers and status files: syscall.Errno (lower case, Go's own
// table), *os.PathError ("open /path: no such file or directory"), *exec.ExitError ("exit status 1",
// "signal: killed") and a program that could not be started ("fork/exec /usr/sbin/x: no such file or directory").

#include <string>
#include <string_view>

namespace nkos::goerr {

inline std::string errno_text(int e)
{
	static const char *const table[] = {
		nullptr,
		"operation not permitted", "no such file or directory", "no such process", "interrupted system call",
		"input/output error", "no such device or address", "argument list too long", "exec format error",
		"bad file descriptor", "no child processes", "resource temporarily unavailable", "cannot allocate memory",
		"permission denied", "bad address", "block device required", "device or resource busy", "file exists",
		"invalid cross-device link", "no such device", "not a directory", "is a directory", "invalid argument",
		"too many open files in system", "too many open files", "inappropriate ioctl for device", "text file busy",
		"file too large", "no space left on device", "illegal seek", "read-only file system", "too many links",
		"broken pipe", "numerical argument out of domain", "numerical result out of range",
		"resource deadlock avoided", "file name too long", "no locks available", "function not implemented",
		"directory not empty", "too many levels of symbolic links",
	};
	if (e > 0 && e < static_cast<int>(sizeof table / sizeof table[0])) return table[e];
	switch (e) {
	case 74: return "bad message";
	case 75: return "value too large for defined data type";
	case 95: return "operation not supported";
	case 110: return "connection timed out";
	case 116: return "stale file handle";
	case 117: return "structure needs cleaning";
	case 121: return "remote I/O error";
	case 122: return "disk quota exceeded";
	case 125: return "operation canceled";
	}
	return "errno " + std::to_string(e);
}

// *os.PathError: "op path: errno".
inline std::string path_error(std::string_view op, std::string_view path, int e)
{
	return std::string(op) + " " + std::string(path) + ": " + errno_text(e);
}

// The name Go prints for a signal (syscall.Signal.String()).
inline std::string signal_text(int sig)
{
	switch (sig) {
	case 1: return "hangup";
	case 2: return "interrupt";
	case 3: return "quit";
	case 6: return "aborted";
	case 9: return "killed";
	case 11: return "segmentation fault";
	case 13: return "broken pipe";
	case 14: return "alarm clock";
	case 15: return "terminated";
	}
	return "signal " + std::to_string(sig);
}

// *exec.ExitError for a wait status (os.ProcessState.String()).
inline std::string exit_text(int exit_code, int signal)
{
	if (signal) return "signal: " + signal_text(signal);
	return "exit status " + std::to_string(exit_code);
}

// exec.Cmd.Start of a program that cannot run: "fork/exec PATH: errno".
inline std::string start_error(std::string_view path, int e) { return path_error("fork/exec", path, e); }

} // namespace nkos::goerr
