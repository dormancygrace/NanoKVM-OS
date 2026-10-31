# Browser interaction checks

`pnpm browser:fixture` serves a development-only workbench at
`http://127.0.0.1:4317/tests/browser/index.html`. Open it in **Chrome Main**.
It uses the production Login, ProtectedRoute, Head, AppTheme, VirtualKeyboard
and WebSocket client, backed by a loopback HTTP/WebSocket fixture. There is no
proxy to a device and no environment-supplied credential. The fixture's only
login is `admin` / `browser-test-password`; this is synthetic test data.

The workbench replaces the video/desktop with a focusable input, so these tests
cover browser input and session behavior, not video decoding or hardware HID.
The existing decoder/worker and server tests still cover those boundaries.

For a local check in your own Main-profile tab:

1. Try a wrong password, then the fixture login. Check that one title and favicon
   remain after changing the title.
2. Focus the video target and open the keyboard. The target stays editable.
   Close using the button and Escape; focus returns. Closing via an outside
   control must leave focus on that control.
3. Press a key and release outside it. The last HID report must contain zeroes
   after its leading `1`. Repeat while blurring/unmounting. Modifier selection
   must clear when the keyboard closes.
4. With touch input at 393 x 852, drag over keys and over the handle. Only the
   handle moves the panel; cancel restores its position, and a 64px downward
   swipe closes it. Reopening starts at zero offset.
5. Drop the connection while a key is held. The UI shows reconnecting, then
   connected; held input must not be replayed. Expire the session and verify
   return to login without further connection attempts.
6. Open password help at a narrow viewport; its content must fit the dialog.
7. In the Network panel, confirm the USB settings module is absent before
   hovering, focusing or opening USB Gadget. Hover or focus the button: the
   module should preload without opening the panel. Open, close and reopen it;
   the module loads once.
   Block its first request and verify the error stays in the popover, the
   desktop remains usable after closing it, and Refresh recovers the panel.

Shutdown confirmation regression: open /tests/browser/shutdown.html in Chrome
Main. The secondary action (labelled **No** in English) must be primary and
focused; Enter closes the dialog with the events list still empty. Reopen and
explicitly click **Shut down NanoKVM**: exactly one prepare request appears,
then the simulated failure is shown. The fixture intercepts all requests and
never commits shutdown or connects to a device.

`pnpm test:browser` runs these cases in CI using installed Google Chrome and a
fresh **Main** profile under the runner's temporary directory. The launcher
refuses to create a substitute local profile. `pnpm test:browser --list` lists
cases without launching a browser. The suite runs serially, has no retries,
and uploads a trace on failure. Test-only files are excluded from the production
bundle and a chunk test checks that the fixture credentials/routes do not ship.

### Add-on form regressions

Open `/tests/browser/addon.html` in the same Chrome Main fixture session. It uses
the production AddonPage and HTTP error interceptor with a controlled adapter;
all data is synthetic and no add-on or device is contacted.

- Save Name `draft A`, change it to `draft B` while Save is pending, then press
  Complete save. After the GET delivery appears in Test events, Name stays B.
- Hold old source, then Switch source. After CURRENT NEW appears, Release old
  read. Name and status stay on the new source.
- On a fresh page, Hold next poll and wait for the held GET event. Save a changed
  Name, Complete save, wait for its refreshed GET, then Release old read. The
  committed Name must remain visible.
- Fail saves, enter the literal synthetic value `fixture-secret`, and Save. The
  refusal remains visible and Test events must not contain PASSWORD LOGGED.

`addon.spec.ts` runs these regressions in CI, and verifies that a read slower
than the polling interval is allowed to finish without overlapping requests.

### Date and time regressions

`/tests/browser/date-time.html` uses the production DateTimeSettings with a
controlled adapter and a synthetic clock. Hold poll / Release read verifies
that a late status cannot undo an immediate selection. Edited NTP servers
must only be sent by Save. Complete write, Refuse writes and NTP synchronized
cover the clock button and its partial-failure message. `date-time.spec.ts`
runs these three scenarios in CI. Neither the host nor a device clock changes.

### BMC settings regressions

`/tests/browser/bmc.html` exercises the production BMC page with synthetic
responses. Hold next read / Release read and Complete write control response
ordering. Check stored account use by switches, serialization through readback,
newer account edits during Save, stale polls, persistent refusal messages,
credential redaction and the initial-password path. `bmc.spec.ts` repeats these
cases in Chrome Main in CI. No device or add-on is contacted.

### Notifications regressions

`/tests/browser/notifications.html` uses the production Notifications page
with a synthetic Telegram channel; Complete save releases a held save. Edit a
channel name, add or delete channels, or pick another message language while
Save is pending: after Complete save the newer edits stay, Save stays
enabled, and the next save sends them with the ids the device gave the
channels already sent. A save with no edit meanwhile disables Save, and
folding a channel is not an edit. Add a template, set the title of "Host
powered off" and pick it on the channel: Preview shows the template's title
(PREVIEW in Test events) and Save sends the template with the channel using
it (TEMPLATES ... USED). Deleting a template returns its channels to the
standard messages; the standard preview follows the message language.
`notifications.spec.ts` runs these cases in CI. No device is contacted.

## Recovery

Open /tests/browser/recovery.html. All recovery calls are synthetic, including
apply and reboot. Use the displayed fixture passwords to check export,
restore/reset previews, cancellation, apply errors and pending reboot.
The fixture archive button supplies a tiny synthetic file; export records
the Blob download without writing into Downloads.

### Updates

Open /tests/browser/updates.html for a synthetic repository and package state.
Offer core update, Complete package check, Complete installation and Fail operation
exercise the production card and its confirmation dialogs. Daily-check settings,
channel switching and stable rollback use the same production page. No package
manager, host reboot or device request is executed. updates.spec.ts covers
these paths in CI.

### Diagnostics

Open /tests/browser/diagnostics.html. The production page receives only synthetic
API responses. Healthy keeps optional stopped services, disabled USB and no HDMI
signal neutral; APK failure and EDID pending must appear in the summary. Multiple
problems must list all eight items. In Logs, search for needle and cycle next and
previous at wide and narrow widths: the highlighted line must remain in the viewer
after long wrapped lines. Check Only matching lines, no matches and clearing search.
These six regressions run in diagnostics.spec.ts in CI. No device is contacted.

### Appearance

Open /tests/browser/appearance.html for the production Appearance page with
synthetic responses. Hold writes makes the color/reset event ordering visible:
after editing a hex color, Restore default must send DELETE on the first click,
and also work after Tab then Enter. Tabbing past Reset without activating it
still saves the draft. Refuse writes + Complete write must restore the confirmed
color and show the error. These four cases run in appearance.spec.ts in CI.
The menu switches and role buttons also allow checking browser-local settings;
no device is contacted.

### Package index schedule

Open `/tests/browser/index-schedule.html` for the production schedule controls
with held, synthetic writes. Edit the time and immediately click the switch:
one request must contain both changes. Tab through the switch, save with Enter,
leave the time incomplete, and refuse a write to check focus, serialization and
rollback. `index-schedule.spec.ts` covers these cases in CI.

### HDD LED responsiveness

`/tests/browser/leds.html` uses the production Power toolbar with synthetic event
messages and a local HTTP adapter; it never connects to a device. **8 ms blink**
and **Combined on/off** must each produce a short amber flash. **Activity on**
remains steadily amber, without a decorative pulse. **Drop stream** reconnects
after three seconds; **Toggle toolbar** closes its connection. The connection
and fallback-poll counters help verify that opening the Power popover does not
start a second connection or a fast polling loop.
