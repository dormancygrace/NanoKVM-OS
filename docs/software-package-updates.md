# Software package indexes and optional add-ons

Software > Packages can refresh APK indexes automatically once per day. The setting is off by default. Enable the checkbox, select a time, and save. Times use the device timezone, shown beside the field; the browser timezone is irrelevant.

This operation runs only `apk update`. It never installs, removes, or upgrades packages. The device must be running at the scheduled time. A missed run is not replayed after boot; users can refresh manually at any time.

The schedule is owned by the `# nanokvm-apk-index-refresh` marker in the root crontab. NanoKVM preserves unrelated cron jobs and keeps the selected time when the checkbox is disabled. The existing `/etc/crontabs` persistence under `/etc/kvm/cron/crontabs` preserves the setting across application updates and reboots. Enabling the setting enables/starts OpenRC's `crond` service; disabling it removes only this job.

The root-only `nkos-update refresh-indexes` helper checks the setting, takes the OS update lock, and uses the shared, memory-aware APK runner. An overlapping OS update can skip that day's refresh. Operation results and APK output use the Software page's existing status/log files. APK errors do not turn into package installations.

`GET /api/os/update/software/index-schedule` returns the schedule and device timezone. The administrator-only `PUT` accepts an `enabled` boolean and a strict `HH:MM` time. The API uses `crontab` to install the updated file and notify the daemon.

## Add-ons

RustDesk remains a separate optional `nanokvm-rustdesk` APK. It is absent from the standard image and the core release archive. Software > Add-ons can refresh repository indexes with **Check for updates**, display the available package revision, and install the update on request. The package repository must publish the newer APK and its signed index; changing the application version alone does not update an installed add-on.

Before the repository index is signed, every add-on is checked: it may declare only its own files, dependencies and lifecycle scripts. An add-on that has install-if, provides, replaces, triggers or a conflicting dependency is refused. So an add-on can neither install itself on devices that did not choose it nor take the place of a core package.

The acme.sh card in Software > Add-ons uses the same optional installer as Network settings. Its settings shortcut opens the existing HTTPS/ACME controls; it does not install anything or change certificates by itself.

## Explicit RustDesk upgrades

The Update button uses `apk add --upgrade nanokvm-rustdesk`. Older
installations may have an exact version or local-file constraint in APK's
world file. Plain `apk upgrade nanokvm-rustdesk` keeps that constraint and
can exit successfully without installing the offered update. The add command
replaces only this add-on's world constraint as part of APK's transaction.

The server reads the installed version before and after the transaction and
checks that APK considers the new version newer. An unchanged version,
unreadable result or failed transaction is an error, retained on the Add-ons
card until the next action instead of cleared by status polling.

Regression coverage includes the service's error/no-op paths.
