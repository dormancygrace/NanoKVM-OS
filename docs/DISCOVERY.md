# Finding NanoKVMs on the network

A NanoKVM answers for `<hostname>.local` on the local network (mDNS). It also
announces itself as a service, so that it can be found without knowing its
name: a program asks the network which NanoKVMs are there, and each one
answers.

## What is announced

The service type is `_nanokvm._tcp` (DNS-SD). An announcement carries:

- the name of the service: the hostname of the device;
- the host (`<hostname>.local`) and the port of the web interface;
- `proto`: `https` or `http`, as the web interface is served;
- `model`: the board (`PCIE`, `Alpha`, `Beta`).

It is announced by the mDNS service of the device. With mDNS switched off
(**Settings > Network > General**) nothing is announced, and the device still
finds the others.

From a computer on the same network:

```sh
avahi-browse -rt _nanokvm._tcp        # Linux
dns-sd -B _nanokvm._tcp               # macOS, Windows with Bonjour
```

## In the interface

The overview and **Settings > Dashboard** have an **Other NanoKVMs** panel
when the device finds any: each with its name, its board and its addresses,
the name a link to its interface. The device asks the network once a minute
while the page is open. Without other devices there is no panel.

## API

| Request | Who | Result |
| --- | --- | --- |
| `GET /api/network/peers` | signed in | `peers`: `name`, `host`, `addresses` (IPv4), `port`, `proto`, `model` and `self` (this device) of every NanoKVM that answered within about a second |
