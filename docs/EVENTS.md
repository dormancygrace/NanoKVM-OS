# Event log

Settings > System > Event log shows what happened to the host and who did what to it: the record a server's BMC keeps as its event log. The server keeps it as a journal on the card (`server/journal`).

## What is recorded

| Event | When |
| --- | --- |
| Host powered on, powered off | the power LED changed and stayed so for 5 s (needs the LED wired and ATX on) |
| HDMI signal lost, restored | while video capture runs for any reason (a viewer, the screen watch); capture is not kept running for the journal |
| Blue screen, kernel panic on the host | the screen watch of the TPU add-on saw one; the text read off the screen is kept with it |
| Login from a new address, logins blocked, attempts limited | the web login, Redfish and IPMI |
| Button pressed | a press of the power or reset button, with its length and who asked: a web user, Redfish, IPMI, MCP |
| Image mounted, image ejected | the virtual drive's medium changed through the web page (with the user) or through MCP ("API") |

The first four groups are the events of Settings > Notifications. They are recorded whether or not a notification channel takes them, after the same holding back of repeats: a login event an outsider can cause at will is recorded once per its window, as it is sent once.

## Bounds

- The newest 500 events are kept, in `/var/lib/nanokvm/events.jsonl` (one JSON object a line, readable by root). The file is rewritten from the newest 500 when it holds 1000.
- At most 30 events are added in a minute. What is left out is counted, and the next event is preceded by "Events not recorded" with the count.
- One event is one short synced write to the card; events are rare.
- The text read off a crashed screen is cut at 2000 bytes.

The events are stored as message keys with their values and put into words when shown, in the page's language (English where a language has no text for a message).
