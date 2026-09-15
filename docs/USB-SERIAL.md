# Optional USB Serial resize bridge

The browser includes opt-in size negotiation for a receiving Linux host.
The helper runs on that host, not on NanoKVM, and requires Python 3 and
util-linux agetty/login. Normal login authentication is retained.

Install `firmware/services/nanokvm-serial-bridge.py` as
`/usr/local/sbin/nanokvm-serial-bridge.py` on the receiving host and make it
executable. Install `firmware/services/serial-getty-resize.conf` as a drop-in
for the correct `serial-getty@DEVICE.service`, then reload systemd and restart
that service. Restarting ends its existing login session; select the actual
host serial device. Remove only this drop-in to disable the extension.

The helper wraps agetty with a PTY and exchanges nonce-bound OSC 777 frames.
Browser resize updates the PTY size and sends SIGWINCH to the foreground
program. Plain consoles receive no resize replies without a bridge request.
This protocol is for interactive terminals, not binary file transfer, and
multiple simultaneous readers of one serial device are not supported.

Opening NanoKVM's USB ttyGS0 terminal sends one Enter. An already active shell
or application receives that Enter; reconnecting does not reset its session.
