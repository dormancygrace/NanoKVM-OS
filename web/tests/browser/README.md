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
