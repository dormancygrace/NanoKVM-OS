# Add-on pages

An add-on can add a page to the web UI's settings, or a section to one of its pages, without a change to NanoKVM OS, much as OpenWrt's LuCI apps do. It ships two things:

- **A manifest**, `/usr/share/nanokvm/addons.d/<id>.json`, a file of its package. It names the add-on, its settings page and its API socket.
- **An HTTP API on a unix socket** in `/run/nanokvm/addons/`, served by the add-on's own daemon.

NanoKVM OS draws the page from the form the manifest describes. Its values come from the add-on's API, which administrators reach through the server at `/api/addon/<id>/...`.

The server reads the manifests on every request: an installed or removed add-on shows up or goes away without a restart. An invalid manifest is left out and reported once in the server log; the other add-ons still show.

The IPMI add-on (`addons/ipmi/addon-ui.json`, `cmd/nkos-ipmi/status.go`) is the working example: a section in the IPMI tab of Services > Redfish and IPMI.

## Manifest

```json
{
  "id": "ipmi",
  "api": 1,
  "title": { "en": "IPMI", "ru": "IPMI" },
  "description": { "en": "IPMI over LAN for power control of the host." },
  "backend": { "socket": "ipmi.sock" },
  "settings": {
    "section": "services-bmc",
    "form": {
      "source": "/api/addon/ipmi/status",
      "refresh": 5,
      "fields": [{ "key": "state", "type": "status", "label": { "en": "State" } }]
    }
  }
}
```

| Key | Meaning |
| --- | --- |
| `id` | `[a-z0-9][a-z0-9-]*`, at most 32 characters. The file is named `<id>.json`. |
| `api` | The manifest format, `1`. The server skips a manifest asking for another one, so an add-on made for a newer NanoKVM OS does not show a broken page on an older one. |
| `title`, `description` | Texts by language (see below). The title names the page; the description stands at its top. |
| `backend.socket` | The socket's file name in `/run/nanokvm/addons/`, ending in `.sock`. Only the manifest chooses the socket. |
| `settings.group` | A page of its own, listed at the end of this settings group: `usb`, `network`, `services`, `system`, `ai` or `software`. |
| `settings.section` | Instead of a group: part of this page of NanoKVM OS, for an add-on that extends what the page is about. `services-bmc` (Services > Redfish and IPMI) for now: the add-on gets a tab of its own, titled with its title; the IPMI add-on's status goes into the page's IPMI tab, as a section titled with its title and description. |
| `settings.icon` | For a page of its own, optional: a name from the icon set of the settings menu (`menuIcons` in `web/src/pages/desktop/menu/settings/index.tsx`), such as `server`, `network`, `package` or `shield`. Other names show a puzzle piece. |
| `settings.form` | The page, below. |

**Texts** are objects keyed by the web UI's language tags (`en`, `ru`, `de`, `fr`, `es`, `cz`, `ja`, `zh`, `zh_tw` and the others in `web/src/i18n/languages.ts`). `en` is required. The page shows the UI language, then its base language (`zh` for `zh_tw`), then English.

Unknown keys make the manifest invalid; so do mistakes the server can find (`server/service/addons/manifest.go`).

## Form

| Key | Meaning |
| --- | --- |
| `source` | Answers `GET` with `{"code": 0, "data": {...}}`: the fields' values by key. |
| `submit` | Takes a `POST` of the edited values and answers `{"code": 0}`, or `{"code": -1, "msg": "..."}`, whose message the page shows. Required when the form has an editable field, and not allowed otherwise. |
| `refresh` | Optional: reload `source` every 2 to 3600 seconds while the page is open (and visible). |
| `fields` | 1 to 64 fields, in page order. |
| `actions` | Optional buttons. Each `POST`s with no body to its `url`. A `confirm` text asks first; `danger` marks a destructive one. |

Every URL is a path under `/api/addon/<id>/`, with no query and no `..`. A manifest cannot point the page at the server's own API.

**Fields:**

| `type` | Control | Value |
| --- | --- | --- |
| `text` | text box | string |
| `password` | password box | string; never shown. The form starts empty and sends the field only when something was typed, so the add-on keeps the current password otherwise. `source` should not return it. |
| `number` | number box, `min` and `max` optional | number |
| `select` | list of `options` (`value`, `label`) | string |
| `switch` | switch | boolean; always sent |
| `status` | read-only text in the Status section | anything; `true`/`false` show as Yes/No, an empty value as a dash. Never sent. |

`key` is `[A-Za-z][A-Za-z0-9_]*`. `label` is required, `help` optional; `required` makes a text, number or select field mandatory.

## API

The server passes `/api/addon/<id>/<path>` to the socket as `/<path>`, with the method, the query and the body. It passes only what an administrator's signed-in browser sends. Before passing it on, the server:

- removes the browser's `Cookie` and `Authorization` headers;
- sets `X-NanoKVM-User` and `X-NanoKVM-Role` to the signed-in account (the server checked it; the socket is reachable by root only);
- limits the body to 1 MiB and the answer to 30 seconds;
- drops `Set-Cookie` from the answer and marks it `Cache-Control: no-store`.

When the socket does not answer, the server answers HTTP 502 and the page or section says the add-on's service is not running: a state, not an error (the IPMI service is stopped while IPMI is off). WebSocket and streaming answers are not passed in this version.

Listen with the socket readable by root only (`0600`), and remove a stale socket file before listening.

## Packaging

Add the manifest to `files` in the recipe (`addons/<name>/addon.json`):

```json
"/usr/share/nanokvm/addons.d/ipmi.json": {"source": "addon-ui.json", "mode": "0644"}
```

Removing the package removes the manifest and with it the page. `server/service/addons/manifest_test.go` checks every `addons/*/addon-ui.json` of this repository.

## Not yet

These may come in later versions of the format:

- add-on pages written as code (a web component loaded from the add-on);
- MCP and PicoClaw tools declared by the manifest;
- buttons in the toolbar;
- separate translation files.
