# Running Flex Emulator with Docker

> **Disclaimer:** Independent hobby project, not affiliated with, endorsed by or supported by FlexRadio Systems, Icom, the Hermes Lite project, 4O3A, the Thetis or AetherSDR developers, or anyone else. Developed with AI. **Use at your own risk**, no warranty of any kind. Check that transmit interlock works at your station before relying on it with an amplifier or antenna switching.

Running the emulator in a container on an always-on Linux machine (Unraid, a NAS, a Raspberry Pi, a small server) means it's always there for your Antenna Genius, whichever PC is running your SDR software.

- [What you need](#what-you-need)
- [Docker Compose](#docker-compose)
- [Unraid](#unraid)
- [First-time setup](#first-time-setup)
- [Following a radio over a VPN](#following-a-radio-over-a-vpn)
- [Updating](#updating)
- [Image tags](#image-tags)
- [Settings reference](#settings-reference)
- [Network ports](#network-ports)
- [Security](#security)
- [Troubleshooting](#troubleshooting)

## What you need

- **Docker on Linux.** The container must use host networking (explained below), which only works properly on Linux. Docker Desktop on Windows or Mac is not supported; use the normal Windows or Mac download instead.
- The server must be on the **same network (LAN) as your Antenna Genius** and other Flex accessories.
- A way to reach the radio's data: a TCI server (Thetis, ExpertSDR, AetherSDR), a hamlib `rigctld`, Kenwood-style CAT over TCP, or a radio plugged into the server by USB.

The image is `ghcr.io/jsttylr/flex-emulator` and is built for `linux/amd64` (regular PCs and most servers) and `linux/arm64` (Raspberry Pi 4/5 with a 64-bit OS). Docker picks the right one automatically.

### Why host networking?

A FlexRadio announces itself with a UDP broadcast on port 4992, and accessories then connect back to TCP port 4992. Docker's normal "bridge" networking keeps broadcasts inside the container, so the Antenna Genius would never see the emulator. With `network_mode: host` the container uses the server's own network, so the emulator appears at the server's IP address exactly like a real radio.

## Docker Compose

1. Make a folder for it on the server and put this in `docker-compose.yml` (the same file is in the repository):

   ```yaml
   services:
     flex-emulator:
       image: ghcr.io/jsttylr/flex-emulator:latest
       container_name: flex-emulator
       network_mode: host
       restart: unless-stopped
       environment:
         FLEXEMU_PASSWORD: "change-me"
       volumes:
         - ./config:/config
       # devices:
       #   - /dev/ttyUSB0:/dev/ttyUSB0
   ```

2. Change `change-me` to a password of your own.
3. Start it:

   ```sh
   docker compose up -d
   ```

4. Check it's healthy:

   ```sh
   docker compose ps        # STATUS should say "healthy" after a few seconds
   docker compose logs -f   # live log; Ctrl+C to stop watching
   ```

5. Open `http://<server-address>:8992` in a browser and continue with [First-time setup](#first-time-setup).

Settings and the log file are kept in the `config` folder next to the compose file, so you can update or recreate the container without losing your setup.

**Keeping the password out of the compose file:** put `FLEXEMU_PASSWORD=your-password` in a file called `.env` next to `docker-compose.yml`, and change the line to `FLEXEMU_PASSWORD: "${FLEXEMU_PASSWORD}"`.

## Unraid

The repository includes an Unraid template.

1. Open a terminal on the Unraid server (the **>_** icon at the top right) and run:

   ```sh
   wget -O /boot/config/plugins/dockerMan/templates-user/my-flex-emulator.xml https://raw.githubusercontent.com/jsttylr/flex-emulator/main/unraid/flex-emulator.xml
   ```

   If the pasted command starts with `^[[200~` and fails, the terminal's "bracketed paste" is showing. Run `printf '\e[?2004l'` once, then paste again.

2. Go to **Docker → Add Container**, and pick **flex-emulator** from the **Template** list.
3. Fill in **Settings page password**. Leave **Network Type** on **Host** and **Settings and log** on `/mnt/user/appdata/flex-emulator` unless you have a reason to change them.
4. Click **Apply**. When it's running, click the container's icon and choose **WebUI**.

If you'd rather not use the template, add a container by hand with repository `ghcr.io/jsttylr/flex-emulator:latest`, network type **Host**, a path `/config` mapped to `/mnt/user/appdata/flex-emulator`, and a variable `FLEXEMU_PASSWORD`.

To update on Unraid, use **Check for Updates** on the Docker tab, then **Apply Update**.

## First-time setup

1. Open `http://<server-address>:8992`. Your browser asks for a user name and password: use any user name, and the password you set.
2. Under **Settings**, check **Network address to advertise** shows the server's LAN address. If the server has several network connections (Unraid often has Docker and VM bridges), pick the one on the same network as your Antenna Genius.
3. Set **Name shown to accessories** to something you'll recognise (for example `HL2`).
4. Pick the **Input**. On a server this is usually **TCI** or **Hamlib rigctld**, with **Computer running it** set to the address of the PC running Thetis or your other SDR program.
5. Click **Save and restart**. The radio card at the top should show **Radio connected** and follow your frequency.
6. In the Antenna Genius setup, choose the emulated radio on one of its radio ports.

## Following a radio over a VPN

The emulator only needs to be on the same LAN as the accessories. The link to your SDR software is an ordinary TCP connection, so it can cross a routed VPN such as WireGuard.

Example: Thetis runs at a remote site and controls a Hermes Lite 2 in the shack over WireGuard.

1. On the remote PC, set Thetis's TCI server to listen on `0.0.0.0` (or the PC's WireGuard address) instead of `127.0.0.1`, and allow TCP port 40001 through that PC's firewall.
2. On the emulator's settings page, set the input to **TCI** and **Computer running it** to the remote PC's WireGuard address.
3. Make sure the server can reach that address. With WireGuard, the remote PC's tunnel address has to be in the shack side's `AllowedIPs`. Test with `ping` from the server.

The VPN adds a little delay before the accessories hear about band changes and transmit. Don't change band and key up at the same moment, and test interlock into a dummy load first.

## Updating

```sh
docker compose pull
docker compose up -d
```

Your settings stay in `./config`.

## Image tags

| Tag | What it is |
|---|---|
| `latest` | The newest published release. Use this normally. |
| `0.2.0` (etc.) | A specific release, if you want to stay on one version. |
| `main` | The newest code on the main branch, built on every change. May be less tested. |

## Settings reference

These are set as environment variables in the compose file or the Unraid template. Everything else (radios, inputs, antenna names) is set on the settings page and saved in `/config/config.json`.

| Variable | Meaning | Default in the image |
|---|---|---|
| `FLEXEMU_PASSWORD` | Settings page password. **Required**: the emulator won't start the network settings page without it. | none |
| `FLEXEMU_LISTEN` | Address and port for the settings page. | `0.0.0.0:8992` |
| `FLEXEMU_CONFIG` | Location of the settings file inside the container. | `/config/config.json` |

## Network ports

With host networking these are opened on the server itself. If the server runs a firewall, allow them from your LAN.

| Port | Direction | Used for |
|---|---|---|
| UDP 4992 | out (broadcast) | "I'm a Flex radio" announcements, once a second |
| TCP 4992 | in | Antenna Genius and other accessories connecting to the emulated radio |
| TCP 8992 | in | Settings page |

Only one program on the server can use TCP 4992. If you emulate more than one radio, give each its own API port on the settings page (accessories may only look for 4992, so a second radio on another port might not be found).

## Security

- The settings page can change what your accessories do, so it always needs a password when other computers can reach it.
- The page uses plain HTTP, which is fine on your own LAN. **Don't forward ports 8992 or 4992 to the internet.** For remote access, use your VPN.
- The container doesn't need privileged mode or any extra capabilities.

## Troubleshooting

**The container keeps restarting.** Look at `docker compose logs`. The most common cause is a missing `FLEXEMU_PASSWORD`; the log says so.

**The Antenna Genius doesn't list the emulated radio.**
- Check the container uses host networking (`docker inspect flex-emulator --format '{{.HostConfig.NetworkMode}}'` should print `host`).
- Check the advertised address on the settings page is on the same network as the AG.
- Check nothing else on the server uses TCP 4992. The radio card shows an error if the port is taken.

**The radio card says "Waiting for radio".** The emulator can't reach your SDR software. Check the address and port, that the program's TCI or rigctld server is turned on, and that the PC's firewall allows it. The Activity box shows each attempt and the error.

**The accessory connects but doesn't follow.** Turn off **Only show what accessories send** in the Activity box, reproduce the problem, click **Copy log**, and open an issue with the log attached.

**USB radio not found.** Add the device to the compose file (`devices:`) or the Unraid template, then pick it under **COM / serial port**. Only one program can use a serial port at a time.
