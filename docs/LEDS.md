# ATX LED display

The desktop receives authenticated `GET /api/vm/gpio/events` server-sent events.
Each `data:` message is a current `{ "pwr": boolean, "hdd": boolean }` snapshot,
or `null` when GPIO cannot be read. A new connection receives a fresh snapshot;
there is no historical event replay. Idle connections receive a comment every
15 seconds. Session expiry/revocation, client departure and switching ATX off
terminate the stream. With ATX already off the endpoint returns HTTP 204 and
does not open GPIO lines.

The existing shared GPIO monitor still reads active-low Power and HDD inputs.
It waits for GPIO edges and services a busy line at most once every 20 ms.
A blink that ends before the GPIO level is read still counts as activity. HDD
activity in the event stream remains visible for 60 ms after its last observed
edge, and remains on while the physical input is active. Snapshot readers
(`GET /api/vm/gpio`, live status, BMC and MCP) keep their existing 350 ms
activity window through `Current()`, using the same physical activity timestamps
without another GPIO reader or timer. Boards without edge support retain the
20 ms sampling fallback. Slow web clients read the latest snapshot rather than
queueing events or delaying GPIO monitoring.

The browser shares one event connection across LED consumers while the tab is
visible. It closes the connection when hidden or unmounted, reconnects after an
error, and uses the existing shared live-status poll while events are unavailable.
A received on/off pair remains visible for about 40 ms, including when both
messages arrive in one network read. The HDD icon has no decorative pulse or
color transition. Disconnection and hidden tabs clear transient HDD activity but retain the last
confirmed power level while reconnecting. Missing or older fallback samples do
not replace it. An explicit `data: null` from the server clears both LEDs; the
last unsubscribe also clears stored state.

This is a responsive activity indicator, not a waveform recorder: bursts within
the hold interval merge, and network delay and display refresh still limit how
closely the screen can follow an individual electrical pulse. Power polarity,
pin configuration and the power/reset controls are unchanged.
