# Sign-out after inactivity

A sign-in to the web interface lasts for weeks, so a browser left open stays a way in. **Settings → System → Security → Sign out after inactivity** sets how long a sign-in may show no activity before NanoKVM ends it: 5, 15 or 30 minutes, 1 or 4 hours, or never (the default). An administrator sets it, for every account.

## What counts

Activity is input in the page: the mouse, the keyboard, the wheel, a touch, the remote screen included. What a page does by itself does not count: its status requests, an open video, a terminal that prints. Watching a long installation without touching anything therefore ends the sign-in; choose a limit that fits such work, or none.

Several tabs of one sign-in share its time: input in any of them keeps all of them.

A program that sends its token in an `Authorization` header has no page: each of its requests is its activity, and it signs in again after a pause longer than the limit.

## What happens

In the last minute the page shows the time left; any input keeps the sign-in. When the time is out NanoKVM refuses the sign-in's requests, ends its video, audio, input, terminal and event connections and removes its cookie, and the page opens the sign-in form. A transfer that is under way, an image upload for one, finishes. Other sign-ins of the same account are not touched, and the ended one stays ended.

NanoKVM keeps the clocks, not the browser, so a closed tab or a sleeping laptop is signed out as well. A period starts when the sign-in is made. The clocks are in memory: after a restart of NanoKVM a sign-in made before it gets one period from the restart, an ended one too if the browser still has its cookie. Changing the limit starts the running periods again from the change; a sign-in already out of time stays ended.

The limit is in `/etc/kvm/idle-signout`, in seconds; without the file there is none. Redfish, IPMI and the MCP key have their own sign-ins and are not affected.
