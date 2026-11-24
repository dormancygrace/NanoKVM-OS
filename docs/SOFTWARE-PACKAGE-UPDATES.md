# Software package indexes and packages released on their own

Software > Packages can refresh APK indexes automatically once per day. The setting is off by default. Enable the checkbox, select a time, and save. Times use the device timezone, shown beside the field; the browser timezone is irrelevant.

This operation runs only `apk update`. It never installs, removes, or upgrades packages. The device must be running at the scheduled time. A missed run is not replayed after boot; users can refresh manually at any time.

The schedule is owned by the `# nanokvm-apk-index-refresh` marker in the root crontab. NanoKVM preserves unrelated cron jobs and keeps the selected time when the checkbox is disabled. The existing `/etc/crontabs` persistence under `/etc/kvm/cron/crontabs` preserves the setting across application updates and reboots. Enabling the setting enables/starts OpenRC's `crond` service; disabling it removes only this job.

The root-only `nkos-update refresh-indexes` helper checks the setting, takes the OS update lock, and uses the shared, memory-aware APK runner. An overlapping OS update can skip that day's refresh. Operation results and APK output use the Software page's existing status/log files. APK errors do not turn into package installations.

`GET /api/os/update/software/index-schedule` returns the schedule and device timezone. The administrator-only `PUT` accepts an `enabled` boolean and a strict `HH:MM` time. The API uses `crontab` to install the updated file and notify the daemon.

## Packages released on their own

RustDesk (`nanokvm-rustdesk`), the computer sensors (`nanokvm-host-sensors`)
and the screen model (`nanokvm-model-screen`) are part of every NanoKVM OS:
`nanokvm-release` depends on them, so the image and every update install
them. Each stays off until switched on on its page. They are built and
published by their own workflows, with versions of their own, into the add-on
repository; an OS release neither rebuilds nor republishes them, and their new
versions arrive with the next package update.

The text recognition models (`nanokvm-model-ocr` and one package per
language) are downloaded on request: AI > Text recognition installs or removes
a language. The server refreshes the indexes and simulates the installation
first, and refuses it when it would upgrade, downgrade, replace or remove any
installed package: a model that needs a later NanoKVM OS would otherwise
update the whole OS as a side effect.

ZeroTier (`nanokvm-zerotier`) is an optional add-on too, installed on request
from Settings > VPN > ZeroTier and not part of `nanokvm-release`. Its own
workflow (`zerotier.yml`) builds it from ZeroTier's release archive pinned in
its recipe; once installed, its new versions arrive with package updates.

Before the add-on repository index is signed, every package in it is checked:
it may declare only its own files, dependencies and lifecycle scripts. A
package that has install-if, provides, replaces, triggers or a conflicting
dependency is refused. So it can neither install itself on devices that did
not choose it nor take the place of a core package.
