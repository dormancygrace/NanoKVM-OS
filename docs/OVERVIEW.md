# Overview page

The **remote console** is the page with the video of the computer's screen and
the control of its keyboard, mouse, power and media: what a server's management
controller calls its virtual or remote console. This page names it so; its
address is `/#/screen`.

A NanoKVM is not always watched: it also runs services, reports the state of the
computer it is attached to, and waits. The overview is a start page for that
use. It shows the state of the device, and the remote screen is one click away.

## The pages around it

Every page but the remote screen has the same navigation.

On a wide window it is at the left:

- **Overview** and **Remote console**;
- **Terminal** (an administrator's), **Images** (with the virtual disk in the
  USB composition) and **Event log**, a shortcut to System > Event log;
- every group and page of the settings as one tree;
- above them a search field: it finds the settings pages by their name or the
  name of their group;
- **Reboot** and **Sign out** at the bottom.

The line above the page has its name, the state of the computer's power LED
and the **Power** button (both with ATX power control), and the **Capture**
switch (an administrator's).

On a window narrower than 768 px, a phone, there are three tabs at the bottom
instead:

- **Host**: the picture of the screen and a button that opens it, **Power**,
  **Images**, **Terminal** and **Event log** as buttons, the **Capture**
  switch and what the device knows about the computer;
- **Status**: the panels of the device;
- **Settings**: the search field and every group of the settings as a tile. A
  group opens the list of its pages, and a page opens with a Back arrow to
  that list.

A page of the settings opened from the **Host** or the **Status** tab (the
event log, the arrow of a panel) returns there by its Back arrow.

A round button switches between the light and the dark theme: in the lower
right corner of a wide window, at the right end of the title line of a narrow
one.

## What the overview shows

- **Remote console**: a small still picture of what the computer shows, renewed
  every few seconds, with an **Open** button. A click on the picture or the
  button opens the remote screen with video and control.
- **Host**: the power and disk LEDs of the computer (with ATX power control)
  and the image it is given (with the virtual disk). Without both there is no
  panel.
- **The panels of Settings > Dashboard**: processor, memory, storage, the
  device, video, network interfaces, VPN and the rest. They update while the
  page is open.
- **UPS** (an administrator's), once the UPS service reads a UPS: whether it
  runs on mains or on battery, the charge, the runtime, the load and the
  model.
- **Services** (an administrator's): LLDP, SNMP and UPS, those that are
  switched on, each with whether it runs and a way to its page; under LLDP the
  first neighbour that announced itself and its port.
- **Recent events** (an administrator's): the five newest entries of the
  event log, with a way to the whole log.

The overview takes no part in the video: it starts no stream, takes no input
and is not counted in the active video sessions. The picture is taken at most once in five seconds however many browsers have the page
open, and only while a page is visible.

A panel is there for what is in use. With the capture switched off the
**Remote console** and **Video** panels are not shown, and the other panels
move into their place. The **Capture** switch turns it on and off; the
overview does not switch the capture on by itself. The VPN panel lists the
installed VPNs, OpenVPN (installed with the system) once it has a profile, and
is not shown without one to list; a network interface that is switched off
has no panel.

The picture is renewed next to a video session as well. It is taken from a
scaler channel of its own, set up for the one picture: the outputs the video
reads and its encoder are not touched. A picture that could not be renewed
(the device does not answer) is marked as possibly out of date.

From the remote screen, the **Overview** button of the menu bar leads back.

## Settings as pages

Beside the navigation the settings are pages, not a dialog: the same pages,
each with its own address (`/#/settings/network-wifi`), so the browser's Back
and a bookmark work. `/#/settings` shows every group as a tile and
`/#/settings?g=network` the pages of one group. An address that names no page
opens System > Device.

A page that is at work (an installation, a connection being made) locks the
navigation, as it does in the dialog: the links, the tree, the search results,
the tabs, **Reboot**, **Sign out** and the browser's Back do not leave it until
it is done. An address typed into the browser cannot be held back.

Over the remote screen the settings stay the dialog they were.

## Start page

**Settings > Appearance > Start page** chooses what opens after the sign-in,
for everyone who opens the device:

- **Overview**: the page above.
- **Remote console**: the video at once, with the menu bar over it.

A device installed from an image that has the overview starts with it. A
device that was installed earlier and updated keeps opening the remote screen
until the setting is changed: `/boot/image.json`, which records the first
installation and is not replaced by updates, carries `"start_page"` for new
images. The choice is saved with the other appearance settings and is part of
a settings backup.

The pages have their own addresses whatever the setting: `/#/overview`,
`/#/status` (the Status tab of a narrow window; a wide one shows the overview
there), `/#/screen` and `/#/settings/<page>`.

## API

| Request | Who | Result |
| --- | --- | --- |
| `GET /api/branding` | anyone | includes `startPage`: `overview` or `screen` |
| `POST /api/branding/start-page` `{"page": "overview"}` | administrator | saves the start page |
| `GET /api/vm/screen/preview` | signed in | `state` (`ok`, `disabled`, `no-signal`, `unavailable`) and, with `ok`, a JPEG of at most 640 × 360 as base64 in `jpeg` |
