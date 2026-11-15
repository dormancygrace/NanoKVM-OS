# 🤝 Contributing to NanoKVM OS

Test reports, translations and code are all welcome. No coding is required to help.

<a id="test-reports"></a>

## 🧪 Test reports

Reports from real Cube, Lite and PCIe devices help most — a successful test is worth reporting too. You do not need to test every feature.

1. Install the [current release](https://github.com/dormancygrace/NanoKVM-OS/releases/latest) with the [installation](README.md#install) or [update](README.md#updates) instructions.
2. Try what you normally use: boot and network access, video and audio, keyboard/mouse, USB media, and OLED or ATX controls where your board has them.
3. [Share a test report](https://github.com/dormancygrace/NanoKVM-OS/issues/new?template=hardware_compatibility.yml&title=Hardware%20test%20report%3A%20).

Include:

- **Board and revision**, and the HDMI receiver if known.
- **OS version** from **Settings → About**, and the **initial installation** from **Settings → System → Diagnostics**.
- Fresh installation or upgrade.
- Browser and computer operating system.
- Resolution/FPS, codec and transport you tried.
- The features you tried, separated into **Works**, **Fails** and **Not tested**.
- For a failure: steps to reproduce and the relevant logs from **System → Diagnostics** (see [logs](docs/LOGS.md)).

Report the exact version you tested, not “latest”. Test a feature from an unreleased PR only with the build and instructions given in that PR. Already following an issue? Add your results there instead of opening a new one. Remove passwords, tokens and private screen contents before posting.

Bugs and other questions go to the [issue tracker](https://github.com/dormancygrace/NanoKVM-OS/issues).

## 🌍 Translations

Translations are maintained on [Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/), in the browser, without git. See [TRANSLATING.md](TRANSLATING.md).

## 💻 Code

See [Building NanoKVM OS](docs/BUILD.md) for the full image build and for working on the web UI or server alone. Open a pull request against `main` with a short description of the change and how you checked it.
