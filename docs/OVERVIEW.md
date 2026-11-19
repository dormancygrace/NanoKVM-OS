# Overview page

A NanoKVM is not always watched: it also runs services, reports the state of the
computer it is attached to, and waits. The overview is a start page for that
use. It shows the state of the device, and the remote screen is one click away.

## What it shows

- **The bar**: the device name, the sections of the settings, the terminal,
  the power buttons (with ATX control), the settings and the sign-out.
- **The panels of Settings > Dashboard**: processor, memory, storage, the
  device, video, network interfaces, VPN and the rest. They update while the
  page is open.
- **Remote screen**: a small still picture of what the computer shows, renewed
  every few seconds, with an **Open** button. A click on the picture or the
  button opens the remote screen with video and control.

The overview takes no part in the video: it starts no stream, takes no input
and is not counted in the active video sessions. The picture is taken like a
screenshot, at most once in five seconds however many browsers have the page
open, and only while a page is visible.

When the capture is switched off, the picture is replaced by that state, and
an administrator gets the **Enable capture** button. The overview does not
switch the capture on by itself. A 3840 × 2160 input has no still picture
(MJPEG is not available at that size); the screen opens as usual.

From the remote screen, the **Overview** button of the menu bar leads back.

## Start page

**Settings > Appearance > Start page** chooses what opens after the sign-in,
for everyone who opens the device:

- **Overview**: the page above.
- **Remote screen**: the video at once, with the menu bar over it.

A device installed from an image that has the overview starts with it. A
device that was installed earlier and updated keeps opening the remote screen
until the setting is changed: `/boot/image.json`, which records the first
installation and is not replaced by updates, carries `"start_page"` for new
images. The choice is saved with the other appearance settings and is part of
a settings backup.

Both pages have their own address whatever the setting: `/#/overview` and
`/#/screen`.

## API

| Request | Who | Result |
| --- | --- | --- |
| `GET /api/branding` | anyone | includes `startPage`: `overview` or `screen` |
| `POST /api/branding/start-page` `{"page": "overview"}` | administrator | saves the start page |
| `GET /api/vm/screen/preview` | signed in | `state` (`ok`, `disabled`, `no-signal`, `blocked`, `unavailable`) and, with `ok`, a JPEG of at most 640 × 360 as base64 in `jpeg` |
