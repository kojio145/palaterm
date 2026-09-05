# PalaTerm

**Parallel SSH / Telnet / Serial access — batch login tool for network engineers.**

日本語版は [README.ja.md](README.ja.md) を参照してください。

PalaTerm logs in to many network devices at once to collect logs, run command sets,
and apply configuration changes in parallel. It ships as a single portable
Windows exe — no installation required.

> **Status: v1.3 (beta).** It works, but expect rough edges. Feedback via Issues is welcome.

## Features

- 🖥 **Parallel login** — connect to every device in your list at once (concurrency is adjustable)
- 📡 **Three transports** — SSH / Telnet / serial console (COM ports auto-discovered)
- 🏢 **Jump host support** — multi-hop connections through bastion servers
  (SSH public-key auth: Ed25519/RSA/ECDSA, passphrase-protected keys supported)
- 🤖 **Login automation** — 14 built-in OS profiles (Cisco IOS/ASA, JUNOS, FortiGate,
  HPE, NEC IX, Yamaha RTX, …) handling enable escalation and pager disabling; all
  profiles are editable and you can add your own
- 📋 **Batch command execution** — assign named command sets per device and run them in one go
- 📁 **Automatic log saving** — `{host}_Config_{date}_{time}.txt` style, customizable with placeholders
- 🔒 **Encrypted credential vault** — AES-256-GCM protected by a master password (nothing stored in plain text)
- 💻 **Interactive terminal** — automate the login to a single device, then take over
  manually in a built-in terminal (xterm.js)
- 🌐 **English / Japanese UI**
- 🚫 **Zero external communication** — no telemetry, no auto-update; designed for use inside closed networks

## Requirements

- Windows 10 / 11 (x64). Uses the WebView2 runtime, which is preinstalled on Windows 10/11.

## Installation

None needed. Download `PalaTerm.exe` from [Releases](../../releases) and run it.

Unsigned executables may trigger a Microsoft Defender SmartScreen warning on first
launch ("More info" → "Run anyway"). Code signing is planned.

## Documentation

- [User manual](docs/manual.html) (Japanese)
- [Usage guide](docs/USAGE.md) / [Build instructions](docs/BUILD.md)
- [Security review record](docs/SECURITY.md) — see [SECURITY.md](SECURITY.md) for how to report vulnerabilities

## Support

This is a spare-time project. Issues and feature requests are read, but responses
and fixes happen on an irregular schedule — please don't expect same-day answers.

## License

MIT License — commercial use permitted. See [LICENSE](LICENSE).
Bundled third-party licenses: [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

**Disclaimer**: This tool automates connections to and configuration changes on
network equipment. The author accepts no liability for any damage arising from its
use. Before using it against production devices, test in a lab environment and
confirm your organization's rules.
