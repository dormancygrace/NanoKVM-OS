# Scheduled tasks

**Settings → Services → Scheduled tasks** runs a script of the Scripts window
(see [SCRIPTS.md](SCRIPTS.md)) or a shell command on a schedule, with BusyBox
`crond`. Tasks run as root, in the device's time zone (Settings → System →
Date and time).

## Schedules

The editor makes the usual schedules: every few minutes, every hour at a
minute, every day, on chosen days of the week, or on a day of every month, at
a time. **Cron expression** takes the five fields themselves: minute, hour,
day of month, month, weekday (0 = Sunday), with `*`, lists `1,3`, ranges
`1-5`, steps `*/15` or `8-18/2`, and three-letter month and weekday names,
as BusyBox `crond` reads them. With both day fields restricted a day matches
either of them. The editor shows the next runs the device computes.

## What runs

- A script runs in its own directory: a `.sh` script needs a `#!/bin/sh` (or
  `#!/bin/bash`) first line; a `.py` script runs with `/usr/bin/python3`.
- A command is one line for `/bin/sh -c`, run in `/root`.

**Run now** starts a task at once, as cron would. Each run keeps its state in
`/run/nanokvm/tasks` (RAM, cleared at boot): the start, the exit code and the
first 64 KiB of its output (standard output and errors together; the rest is
read and dropped, so a talkative task never blocks). **Output** shows it.

## Where tasks are kept

Tasks are lines of root's crontab (`/etc/crontabs/root`, kept under
`/etc/kvm/cron` across updates and by settings backups): a
`# nanokvm-task {...}` comment holds each task's settings, and an enabled task
has its job line after it. Other jobs in the file (Alpine's periodic jobs, the
package index refresh, lines added over SSH) are shown read-only and never
changed. Enabling a task enables and starts `crond`.
