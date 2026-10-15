# Flex Emulator

> **Please read:** This is an independent hobby project. It is not made, endorsed or supported by FlexRadio Systems or 4O3A; FlexRadio, SmartSDR, Antenna Genius, Tuner Genius and Power Genius are their trademarks. The code was written with AI assistance (Claude) and has only been tested at one station. It comes with **no warranty**: before you rely on it with an amplifier or antenna switching, check that transmit interlock works at your station. Licensed under GPL-3.0 (see below).

Makes a non-Flex radio (IC-7300, Hermes Lite 2 in Thetis, anything with CAT) look like a FlexRadio on your network, so a 4O3A Antenna Genius (and other Flex-aware accessories) follow it.

## How it works

1. Every second it broadcasts a FlexRadio "I'm here" packet (UDP port 4992), the same as a real Flex.
2. The Antenna Genius sees it, connects to it (TCP port 4992) and subscribes to slice and transmit status, exactly as it does with your 6600 or 8600.
3. The emulator reads frequency, mode and PTT from your real radio and sends them on as Flex "slice" and "interlock" messages.

## First run (Windows)

1. Download `flex-emulator-windows.zip` from the latest release on the [Releases page](https://github.com/jsttylr/flex-emulator/releases), right-click it, choose **Extract All**, and put the folder somewhere permanent, for example `C:\Ham\FlexEmulator\`.
2. Double-click `flex-emulator.exe`. Windows may say "Windows protected your PC": click **More info**, then **Run anyway**. (The file isn't code-signed yet.)
3. When Windows Firewall asks, tick **Private networks** and click **Allow**. Without this the Antenna Genius can't connect.
4. Your web browser opens the settings page (http://127.0.0.1:8992). There is no program window: the emulator runs in the background. Double-click the program again any time to reopen the settings page, and use **Stop emulator** on the page to stop it.

## Trying it without a radio

The first radio starts in **Manual** mode. Use the band buttons on the settings page and watch whether the Antenna Genius switches. In the AG's own setup, choose the emulated radio ("IC-7300" by default) on a radio port, the same way you picked your Flexes.

## Connecting a real radio

On the settings page, pick an **Input**:

| Radio | Best input |
|---|---|
| IC-7300 with nothing else using its COM port | Icom CI-V, USB cable |
| IC-7300 shared with logging / WSJT-X | Hamlib rigctld |
| Hermes Lite 2 in Thetis | TCI (turn on Thetis's TCI server) or Kenwood CAT |
| Anything hamlib supports | Hamlib rigctld |

Then click **Save and restart**.

## If something doesn't follow

The **Activity** box shows every command the accessory sends (lines with ←). Click **Copy log** and paste it into the project thread.

## Other platforms

From the same Releases page:

- Mac (Apple Silicon): `flex-emulator-mac-apple-silicon`
- Mac (Intel): `flex-emulator-mac-intel`
- Linux PC: `flex-emulator-linux`
- Raspberry Pi (64-bit OS): `flex-emulator-raspberry-pi`

Settings are saved in your user settings folder (`%AppData%\flex-emulator\config.json` on Windows). A log file, `flex-emulator.log`, sits next to it.

## Building from source

Install Go 1.24 or newer, then run `go build .` in this folder. Run `go test ./...` to run the tests.

Every push to `main` is built and tested on GitHub. Publishing a release on GitHub (Releases → Draft a new release) builds all the downloads and attaches them to it.

## License

Copyright (C) 2026 Justin and contributors.

This program is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version. It is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See [LICENSE](LICENSE) for the full text.
