# ReoStream

ReoStream is a proxy for Reolink cameras. It talks to the cameras over Reolink's
own Baichuan protocol and serves the video as RTSP and HTTP MPEG-TS. Frigate,
go2rtc, Home Assistant, Blue Iris or another NVR can then read a stream that does
not have the green blocks, smearing and dropped frames some Reolink models
produce over their own RTSP server.

This app runs the upstream ReoStream daemon on your Home Assistant host. Camera
discovery, battery cameras and the optional FTP clip receiver need the app to be
on your LAN, so the app uses host networking.

## Install

1. Open **Settings → Apps → Install app** in Home Assistant.
2. Open the **⋮** menu and choose **Repositories**.
3. Add `https://github.com/YamanKoudmani/reostream-ha`.
4. Install **ReoStream**, then start it.
5. Open the app and click **Open Web UI**.

The first start creates `/data/config.toml`, generates a 24-character control
panel password, prints it in the app log, and starts the daemon without the
normal one-time claim step.

## First start

Open the app's **Log** tab and look for:

```
ReoStream control-panel password: ...
```

Use that password at `http://<home-assistant-ip>:8562`.

You can choose the password before the first start instead: open the app's
**Configuration** tab and set **Control-panel password** to at least 16
characters. When this option is set, it is applied on every start and replaces a
password changed in the web UI.

### Lost the password

Set **Control-panel password** in the app configuration to a new value of at
least 16 characters, then restart the app. The startup script updates the
`[control]` section of `/data/config.toml` and applies the new password.

If you leave the option empty, the password is managed by the app's
**Settings → Password** page.

## Add cameras

In the ReoStream web UI, choose **Add camera**. The setup page scans the LAN for
wired Reolink cameras on port 9000 and for battery cameras by their UDP search.
Enter each camera's password and save.

Host networking is required for that search to see your LAN. If a camera is on a
different subnet, or the scan does not find it, add it by address instead:

```
[[camera]]
name = "driveway"
address = "192.168.1.50"
username = "admin"
password = "camera-password"
streams = ["main", "sub"]
rtsp = ["main", "sub"]
```

You can edit `config.toml` from the app's **Settings → Config file** page. The app
reloads only the cameras that changed when you save.

Cameras behind a Reolink NVR or Home Hub need one block per camera, all using the
recorder's address and login, with a different `channel` starting at 0:

```
[[camera]]
name = "porch"
address = "192.168.1.20"
username = "admin"
password = "nvr-password"
channel = 0
streams = ["main", "sub"]
rtsp = ["main", "sub"]
```

## Stream URLs

Replace `<ha-ip>` with the address of your Home Assistant host and `<cam>` with
the camera name you chose.

| Stream | RTSP, port 8561 | HTTP MPEG-TS, port 8560 |
|---|---|---|
| Main | `rtsp://<ha-ip>:8561/<cam>` | `http://<ha-ip>:8560/<cam>.ts` |
| Sub | `rtsp://<ha-ip>:8561/<cam>_sub` | `http://<ha-ip>:8560/<cam>_sub.ts` |
| Extern | `rtsp://<ha-ip>:8561/<cam>_extern` | `http://<ha-ip>:8560/<cam>_extern.ts` |
| TrackMix zoom | `rtsp://<ha-ip>:8561/<cam>_zoom` | `http://<ha-ip>:8560/<cam>_zoom.ts` |

Also on port 8560:

| Path | What it is |
|---|---|
| `/<cam>.keyframe` | Latest keyframe, raw |
| `/<cam>.frames` | Raw access units with a 20-byte header |
| `/<cam>.still.ts` | Latest keyframe as a one-frame MPEG-TS |
| `/api/status` | Per-stream JSON status |
| `/metrics` | Prometheus metrics |

RTSP and HTTP streaming are deliberately unauthenticated, because that is what a
recorder needs. The control panel on port 8562 has its own password.

## Home Assistant

For a simple camera entity, use Home Assistant's generic camera with the RTSP
URL:

```yaml
camera:
  - platform: generic
    name: Driveway
    stream_source: rtsp://192.168.1.10:8561/driveway
```

For lower latency, multiple viewers and a stream shared with Frigate, use go2rtc
and point Home Assistant's camera at go2rtc:

```yaml
go2rtc:
  streams:
    driveway: rtsp://192.168.1.10:8561/driveway
    driveway_sub: rtsp://192.168.1.10:8561/driveway_sub
```

Then use `rtsp://127.0.0.1:8554/driveway` or the go2rtc WebRTC URL in Home
Assistant, depending on how your go2rtc installation is set up.

The app can also publish the cameras' own motion, person, vehicle and animal
detections to MQTT. Configure the broker under **Settings → Events** in the
ReoStream UI, or in `config.toml`:

```toml
[events]
mqtt = "192.168.1.10:1883"
mqtt_user = "reostream"
mqtt_password = "broker-password"
prefix = "reostream"
```

Topics are `reostream/<camera>/<motion|person|vehicle|animal>` with retained
`ON`/`OFF`, plus `reostream/<camera>/event` as JSON.

## Frigate

Point go2rtc at ReoStream and point Frigate at go2rtc, so one camera connection
serves detect, record and live view:

```yaml
go2rtc:
  streams:
    driveway:
      - rtsp://192.168.1.10:8561/driveway
      - ffmpeg:driveway#audio=opus
    driveway_sub:
      - rtsp://192.168.1.10:8561/driveway_sub

cameras:
  driveway:
    ffmpeg:
      inputs:
        - path: rtsp://127.0.0.1:8554/driveway_sub
          input_args: preset-rtsp-restream
          roles: [detect]
        - path: rtsp://127.0.0.1:8554/driveway
          input_args: preset-rtsp-restream
          roles: [record, audio]
```

If Frigate runs as an app on the same Home Assistant host, use the host's LAN
address in the ReoStream URL. Without go2rtc, Frigate can read
`http://192.168.1.10:8560/driveway.ts` directly.

The ReoStream UI's **Settings → Connect to NVR** page generates the URL block and
can merge it into an existing Frigate config.

## Networking and ports

The app uses host networking for three reasons:

- the setup page finds wired cameras by scanning port 9000;
- battery cameras are found by their UDP search and woken over the LAN;
- cameras can upload clips to the FTP receiver.

| Port | Use |
|---|---|
| 8560/tcp | HTTP MPEG-TS, snapshots, status API |
| 8561/tcp | RTSP |
| 8562/tcp | Web control panel |
| 2121-2131/tcp | Optional FTP receiver for camera-uploaded clips |

If Home Assistant is reachable from the internet, do not forward 8560, 8561,
8562 or 2121-2131. Use a VPN or Home Assistant Cloud for remote access. The
stream ports are unauthenticated by design, and the control panel is protected
only by its own password.

If a port is already in use, stop the conflicting service or adjust the
ReoStream `listen` addresses in `config.toml`. Port changes need an app restart.

## Data and backups

Everything persistent lives under the app's `/data` directory:

| Path | Contents |
|---|---|
| `/data/config.toml` | Cameras, events, FTP, clock sync and control-panel settings |
| `/data/.control_password` | Generated control-panel password, if the app created one |
| `/data/stills` | Last picture from each battery camera |
| `/data/clips` | Clips saved by the FTP receiver, if enabled |
| `/data/config.toml.bak` | Backup made when the app configuration changed the password |

Home Assistant backups include the app's `/data` directory. To move an install,
restore the app data, or copy `config.toml` into a fresh install.

## Updating

The app is versioned independently of upstream ReoStream. Install updates from
the app store as usual. If you build from source, rebuild the app image after
pulling:

```bash
docker build -t reostream-ha:dev ./reostream
```

The `VERSION` build argument sets the version shown in the ReoStream UI.

## Troubleshooting

**The web UI does not open.** Check the app log for a startup error. A password
shorter than 16 characters is rejected. A config file with a syntax error or an
unknown key also stops startup; fix it in the app's **Config file** page or
restore `/data/config.toml.bak`.

**The app cannot find cameras.** Confirm the camera is powered, on the same LAN,
and reachable on TCP port 9000. Host networking must be enabled (the app sets it,
but a custom network configuration can override it). Cameras on a separate VLAN
need firewall rules for discovery and for the stream connections.

**A camera refuses connections after a restart.** ReoStream normally sends a
stream-stop message for every stream on shutdown, and the app gives it 15 seconds
to finish. A forced kill can leave a camera refusing new sessions for a few
minutes. Wait for the camera to time out, then start the app again.

**The control panel password is rejected after an update.** If the
`control_password` app option is set, it wins over a password changed in the UI.
Clear or change the option, or use the password from the option.

**Frigate shows no audio.** Add the `ffmpeg:...#audio=opus` line to go2rtc, or
read the HTTP stream directly. Not every Reolink model sends audio on every
stream.

**A battery camera is always awake.** Check that `always_on` is not set unless the
camera has constant power. Battery cameras normally sleep between viewers and
motion events.

## Credits and licence

ReoStream is Copyright (c) 2026 Riley Poe and is licensed under the GNU Affero
General Public License v3. Upstream:
[VoltTech21/reostream](https://github.com/VoltTech21/reostream).

This Home Assistant packaging is published at
[YamanKoudmani/reostream-ha](https://github.com/YamanKoudmani/reostream-ha) with
the complete corresponding source under `reostream/`.
