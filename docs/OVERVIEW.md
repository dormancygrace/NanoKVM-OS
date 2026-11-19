# Overview page

A NanoKVM is not always watched: it also runs services, reports the state of the
computer it is attached to, and waits. The overview is a start page for that
use. It shows the state of the device, and the remote screen is one click away.

## What it shows

- **The bar**: the device name, the sections of the settings, the terminal,
  the power buttons (with ATX control), the theme button, the settings and the
  sign-out.
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

While someone has the remote screen open, no new picture is taken: a still of
its own size would make the capture set up the video encoder again under that
session. The overview keeps the last picture and says that it is not renewed,
or, without an earlier one, that the screen is open in a video session.

With frame detection on, a screen that does not change gives no new frame: the
last picture is what it shows, and it stays. "No video signal" is shown only
when the device reports no signal.

From the remote screen, the **Overview** button of the menu bar leads back.

## Settings as pages

Under the bar the settings are pages, not a dialog: the same navigation on the
left and the same pages, each with its own address (`/#/settings/network-wifi`),
so the browser's Back and a bookmark work. **Overview** at the top of the
navigation returns to the overview. An address that names no page opens
System > Device.

A page that is at work (an installation, a connection being made) locks the
navigation, as it does in the dialog: the bar, links from elsewhere and the
browser's Back do not leave it until it is done. An address typed into the
browser cannot be held back.

Over the remote screen the settings stay the dialog they were.

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

The pages have their own addresses whatever the setting: `/#/overview`,
`/#/screen` and `/#/settings/<page>`.

## API

| Request | Who | Result |
| --- | --- | --- |
| `GET /api/branding` | anyone | includes `startPage`: `overview` or `screen` |
| `POST /api/branding/start-page` `{"page": "overview"}` | administrator | saves the start page |
| `GET /api/vm/screen/preview` | signed in | `state` (`ok`, `disabled`, `no-signal`, `blocked`, `in-use`, `unavailable`) and, with `ok`, a JPEG of at most 640 × 360 as base64 in `jpeg`; `held` when it is an earlier picture kept while a video session is open |
