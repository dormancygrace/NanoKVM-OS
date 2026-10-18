# Scripts

The **Scripts** window in the toolbar keeps shell and Python scripts on the
device and runs them as root: on demand, or once at every startup.

## Scripts on the device

- **Upload** takes a `.sh` or `.py` file. Scripts are stored in
  `/etc/kvm/scripts`; uploading a file with an existing name replaces it.
- A `.sh` script is executed directly, so it needs a `#!/bin/sh` (or
  `#!/bin/bash`) first line. A `.py` script runs with `/usr/bin/python3`;
  install Python from **Settings → Software** first.
- **Run** waits for the script and shows its output. **Run in background**
  starts it and returns at once; its output is discarded.
- **Delete** removes the script and its startup mark.

Scripts run as root with the full rights of the device. Run only scripts you
have read.

## Run at startup

Open a script and turn on **Run at startup**. The script then runs once after
every boot:

- after the web server is listening, so the script can call the local API;
- in the background, as root, with the script's directory as the working
  directory;
- in a session of its own, so it keeps running when the NanoKVM service is
  restarted or updated, and a service restart does not run it a second time;
- in the order of its name, without waiting for the previous script: a script
  that needs another one, or the network, waits for it itself.

The network may not be up yet when the script starts. Wait for it in the
script, for example:

```sh
#!/bin/sh
# Wait up to two minutes for a default route.
for i in $(seq 120); do
    ip route | grep -q '^default' && break
    sleep 1
done
exec /usr/local/bin/my-agent
```

A long-running script (an agent, a tunnel) can simply keep running; use `exec`
for its last command so that it does not leave a shell behind.

### Output and state

The marks are kept in `/etc/kvm/autostart.list`, one script name per line.
During a boot each marked script has two files in `/run/nanokvm/autostart`
(RAM, cleared at the next boot; `<name>` is the SHA-256 hash of the
script name relative to `/etc/kvm/scripts`):

- `<name>.log`: the first 64 KiB of its output (standard output and errors
  together). Later output is read and dropped, so a talkative script is never
  blocked.
- `<name>.status`: `running`, `exited N` with its exit code, or `error: ...`
  when it could not start (for example, it was deleted or Python is missing).

The Scripts window shows this state under the switch. To stop a script that
is still running, end it from **Terminal**, for example with `pkill -f
my-agent`.

### Earlier versions

Older versions had a `/api/vm/autostart` API that stored scripts in
`/etc/kvm/autostart`, but nothing ran them. The API is gone; mark a script in
the Scripts window instead.
