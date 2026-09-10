# Local Memory UI fixture

Run from web with the project's Linux Node runtime:

```sh
./node_modules/.bin/vite --config test/memory-ui/vite.config.ts
```

Open http://127.0.0.1:18434/test/memory-ui/index.html. Both the UI API origin and
the server bind are forced to local loopback port 18434. The middleware serves
synthetic memory data and never proxies requests to a NanoKVM. Fixture buttons
control local API responses only. Unhandled API paths return an error. This
route is separate from the production entry point and is not included in dist.

The imported Memory component and its Go-limit child are the production source.
The surrounding page is an isolated test surface, not the full settings modal.
Values are synthetic; toggles model request handling, not kernel memory use.

Manual regression sequence:

1. Slow reads 4.5s: wait for data. Prior polling fired every 3s and discarded
   every response; the corrected poll waits for completion before rescheduling.
2. Reset fixture, Fail reads: after a poll the error appears. Recover reads:
   the next successful response clears this read error.
3. Fail changes, then enable ZRAM: rejection remains visible through later
   successful read polls; the switch stays off. Recover reads, retry ZRAM:
   success clears the change error and enables the switch.
4. Enable ZRAM, recompression and SD separately; inspect request log for the
   respective kinds and optional recompress flag. Select 162 MiB for ZRAM.
5. Toggle Reduce application memory; its switch has the visible label as its
   accessible name. Switch English/Russian to check translations.
6. Unavailable ZRAM: enable/size controls are disabled; a saved recompression
   flag can still be cleared. After clearing, that unsupported switch disables.

Stop Vite after testing. These checks prove client behavior with controlled API
responses, not authentication, the full modal/mobile layout or live swap changes.
