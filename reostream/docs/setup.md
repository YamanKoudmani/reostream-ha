# Setup in detail

The README's Quickstart is enough for most installs. This page covers everything around it.

## Ways to run it

| Method | When |
|---|---|
| `docker run` (README Quickstart) | Default. amd64 and arm64 images. |
| `docker compose up` | Same image, using `docker-compose.yml` from this repo. |
| Static binary | No Docker. linux/amd64 and linux/arm64 on every release. |
| systemd | Plain Linux host, with `contrib/reostream.service`. |
| Unraid | Template in `contrib/unraid/`, see below. |
| Build from source | Go 1.26, `go build ./cmd/reostream`. |

### Static binary

```
curl -fsSL -o reostream \
  https://github.com/VoltTech21/reostream/releases/latest/download/reostream-linux-amd64
chmod +x reostream
./reostream -data ./reostream-data
```

It prints the same claim token the container does. No runtime dependencies.

### systemd

Copy the binary to `/usr/local/bin`, create a `reostream` user, and install
`contrib/reostream.service`. It runs with `-data /var/lib/reostream`, which systemd
creates. Read the comment on `KillSignal` and `TimeoutStopSec` before changing either: if
the daemon is killed before it sends its stream-stop messages, the cameras refuse new
connections for several minutes.

## Networking

Host networking (`--network host`, or `network_mode: host` in compose) is the default in
the README, `docker-compose.yml` and the Unraid template. With it, reostream sees your
real network, so:

- the setup page finds wired cameras by scanning for port 9000, and battery cameras by
  their UDP search, which only reaches the local network;
- cameras can upload clips to its FTP receiver (ports 2121-2131).

On bridge networking, map 8560, 8561, 8562 and 2121-2131, and add cameras by address if
the search does not find them.

## Docker volumes

- The container runs as uid 65532. A named volume (as in `docker-compose.yml`) just works.
  A bind mount to a host directory must be writable by that user:
  `chown -R 65532:65532 ./data` before the first run, or the claim fails to save.
- Camera hostnames are resolved with the DNS server Docker gives the container. If your
  host uses a local resolver (systemd-resolved's 127.0.0.53, for one), Docker falls back to
  8.8.8.8 and local names will not resolve: pass `--dns <your DNS server>`, use
  `--network host`, or put IP addresses in the config.
- Mount the whole data directory, not just `config.toml`. Saves are written to a temp
  file and renamed, which needs the directory to be writable.

### Unraid

reostream is in Community Apps: search for **reostream** in the Apps tab and install it.
The claim token is in the container's log (Docker tab, click the icon, Logs).

The template runs the container as `nobody:users` (`--user 99:100` under Extra
Parameters), the user that owns appdata on Unraid, so the folder needs no setup. A
template installed before v0.2.4 lacks that line: add `--user 99:100` under Advanced
View > Extra Parameters, or the claim cannot save its config.

To add the template by hand instead, in the Unraid terminal:

```
wget -O /boot/config/plugins/dockerMan/templates-user/my-reostream.xml \
  https://raw.githubusercontent.com/VoltTech21/reostream/main/contrib/unraid/reostream.xml
```

Then Docker > Add Container, pick `reostream` from the template list, and Apply.
The claim token is in the container's log (Docker tab, click the icon, Logs).

## Claiming

On first start there is no config. The log prints a one-time token
(`XXXX-XXXX-XXXX-XXXX`) and the URL to open. The token is held in memory only, so a
restart before claiming prints a new one.

The claim page fills in a random 16 character password. Keep it unless you have a reason
not to; a password you type must be at least 16 characters. Submitting writes
`/data/config.toml` and the page asks for that password from then on.

### Lost the password

The password is the `password` line under `[control]` in `/data/config.toml`. Change it
there, then restart. With the default named volume:

```sh
docker run --rm -it -v reostream-data:/data alpine vi /data/config.toml
docker restart reostream
```

Only change the line under `[control]`; the ones under `[[camera]]` are camera logins.
With no cameras added yet, it is simpler to start over:

```sh
docker rm -f reostream && docker volume rm reostream-data
```

then run it again and claim with the new token.

## Config file

Cameras can be added from the page or by editing `/data/config.toml`. When editing by
hand, add to the file that is there; do not replace it. A config without a `[control]`
section is treated as unclaimed and sends you back to the claim screen.

```toml
listen = "0.0.0.0:8560"

[rtsp]
listen = "0.0.0.0:8561"

[control]
listen = "0.0.0.0:8562"
password = "$REOSTREAM_CONTROL_PASSWORD"

[[camera]]
name = "driveway"
address = "192.0.2.50"
username = "admin"
password = "$REOSTREAM_DRIVEWAY_PASSWORD"
streams = ["main", "sub"]
rtsp = ["main", "sub"]
```

- A camera's `address`, `username` or `password`, or the control `password`, starting with `$` is read from that environment variable. A variable that is not set stops startup with an error.
- Unknown keys are an error at startup, so a typo fails at boot instead of serving 404s.
- Saving from the page validates first and reloads only the cameras that changed.

### Sideways cameras

The 16 MP dual-lens cameras on Reolink's newer board (Duo 3 hardware IPC_NT17NA616MP, Elite
Pro Floodlight PoE, and the same board sold under other names) send their full 7680x2160
resolution turned 90 degrees, and tell the app to turn it back. reostream reads that at login
and marks the stream to be shown turned (an H.265 display orientation note, not a re-encode),
only while the picture really is taller than it is wide: their lower resolutions and the sub
stream are untouched. ffmpeg honors the note, so Frigate's detection and snapshots see the
picture upright.

`rotate` overrides it for one camera: `rotate = 90`, `180` or `270` (degrees anticlockwise)
to turn any H.265 camera, or `rotate = 0` to leave a camera alone whatever it says.

### Home Hub and NVR cameras

A camera paired to a Reolink Home Hub or NVR is reached through the hub, as a numbered
channel starting at 0. Add one `[[camera]]` block per camera, all with the hub's address
and login, each with its own `channel`:

```toml
[[camera]]
name = "porch"
address = "192.0.2.20"
username = "admin"
password = "$HUB_PASSWORD"
channel = 0
streams = ["main", "sub"]

[[camera]]
name = "yard"
address = "192.0.2.20"
username = "admin"
password = "$HUB_PASSWORD"
channel = 1
streams = ["main", "sub"]
```

Channels are added by hand in the config for now; the Setup page does not ask for one.
Every stream works through the recorder (main, sub, extern, and a TrackMix's zoom), and so
does the camera's settings page: each setting is read and written on that camera's channel.
The pages that set the whole device (date and time, network ports) are refused for a
channel camera, because behind a recorder they would change the recorder itself. Tested on
an RLN8-410 with a TrackMix PoE.

A camera can be on an NVR and reostream at the same time. Each camera serves two main
stream connections, so an NVR recording main plus reostream fits.

#### NVR and Home Hub auto-add changes camera passwords

With "Automatically add devices in LAN" on, a Reolink NVR or Home Hub adopts every camera
on its network that is still on the default login (admin, no password) and replaces that
camera's password with a random one it keeps to itself. Nothing says so, and removing the
camera from the NVR does not put the old password back. reostream then gets
"authentication failed (401)" from a camera that worked the day before.

To avoid it, give each camera a password before an NVR or hub goes on the same network, or
turn that switch off. To recover a camera it has already taken, either add the camera
through the NVR (its address and channel, as above, with the NVR's login), or add it back to
the NVR and use the NVR's own camera settings to set a password you choose.

## Control page

The control port (8562) is the only part with a password. The streaming port (8560) is
open, which is what a recorder needs, so you can firewall the two separately.

It shows each stream's state, plays it live, edits the config, tails the log, and has a
page per camera with Live, Recordings and Settings tabs. Settings > Connect to NVR writes
the stream URLs for Frigate, go2rtc, Blue Iris or Home Assistant, and can merge them into
a Frigate config you paste in, keeping its comments. Writes are labeled by how
sure we are they work (proven, unverified, known inert, unsafe), and a 200 from the
camera is shown as "accepted", not "confirmed", unless it was read back. See
`docs/control.md`.

`listen` without a password refuses to start unless `allow_no_password = true` is set.

## RTSP

On by default at port 8561 (not 8554, which go2rtc uses). A camera's `rtsp` list names
which of its streams are served; cameras added from the page serve all of them. Remove
the `[rtsp]` section to turn it off.

Serves `rtsp://host:8561/<cam>`, `<cam>_sub` and `<cam>_extern`, plus `<cam>_zoom` and
`<cam>_zoom_sub` for a TrackMix's zoom lens (add `"zoom_sub"` to the camera's `streams`
for the lighter one, 896x512, e.g. for Frigate's detect role). TCP interleaved is the default; UDP is offered, but a lost packet is
a corrupt frame.

### Holding the last picture

A camera in privacy mode (the E1 series) stops sending video, and Frigate's ffmpeg keeps
restarting with "crashed unexpectedly" until it comes back. With `hold_picture = true` on
the camera, its RTSP streams keep showing the last picture, once a second, until live video
returns:

```toml
[[camera]]
name = "nursery"
# ...
hold_picture = true
```

It is off by default, because a frozen picture also hides a real outage. The control page
still shows the camera as disconnected while it holds.

### Two-way audio

On a camera with a speaker, each RTSP stream also offers a back channel (the RTSP
extension ONVIF defines for two-way audio), so Frigate's talk button works through go2rtc
with no extra config: no third stream, and nothing in Frigate's `onvif:` section, which is
for PTZ and autotracking and unrelated to this. Talk needs Frigate's WebRTC live view, as
it does for any camera. Only clients that ask for a back channel see it; ffmpeg and other
players get the same streams as before.

reostream checks the camera's own hardware report before offering it. A camera without
a speaker never gets a back channel, because one that is sent a talk session anyway
breaks talk until it reboots. Speech is sent to the camera over its own connection,
opened when audio starts and closed a few seconds after it stops.

## Frames feed

`/<cam>.frames` on port 8560 streams one record per access unit, for clients that feed a
hardware decoder directly. Each record is a 20 byte big-endian header, then the payload
(Annex B video with parameter sets inline, or AAC):

| offset | type | field |
|---|---|---|
| 0 | 2 bytes | magic `RF` |
| 2 | u8 | version, 1 |
| 3 | u8 | kind: 1 keyframe, 2 inter frame, 3 AAC |
| 4 | u8 | codec: 1 H.264, 2 HEVC, 3 AAC |
| 5 | u8 | reserved |
| 6 | u16 | width, 0 until known |
| 8 | u16 | height, 0 until known |
| 10 | u32 | camera time in microseconds (wraps every 71.6 minutes) |
| 14 | u32 | payload length |
| 18 | u16 | server send time, epoch milliseconds mod 65536 |

A subscriber starts at a keyframe and skips to the next one if it falls behind. Served
for the streams in the camera's `rtsp` list.

## Recordings and saved clips

A camera with an SD card gets a Recordings tab: the days with footage, the day's clips on
a timeline filtered by type, and a player with download, in sub or main quality. Clips are
read off the card only when the tab is open.

reostream also has a small FTP receiver. Settings > Auto-save motion clips, switched on, points the
camera's own FTP upload at it, so each recording is copied here as it happens and kept for
a set number of days. Saved clips show first on the Recordings tab and play without asking
the camera. The button writes this section for you:

```toml
[ftp]
listen = ":2121"            # data ports 2122-2131
dir = "/data/clips"
keep_days = 14
```

## Alerts

The cameras' own detections (motion, person, vehicle, animal) are relayed as they happen.
Set it up under Settings > Events, or in the config:

```toml
[events]
mqtt = "192.0.2.5:1883"           # reostream/<camera>/<motion|person|vehicle|animal> ON/OFF, retained, and /event as JSON
frigate = "http://192.0.2.5:5000" # each detection becomes a Frigate event on the camera of the same name
```

Wired cameras report their detections live. A battery camera reports motion through the
clips it uploads; see Battery cameras below.

## Battery cameras

A battery camera (Argus and similar) keeps its TCP ports closed and is reached over UDP by
its UID. The setup page finds it by its UDP search. Added without streams, it sleeps:

```toml
[[camera]]
name = "backyard"
address = "192.0.2.20"
uid = "95270000XXXXXXXX"
password = "$BACKYARD_PW"
```

- Its sub and main streams run only while someone signed in to the control page is
  watching: Play on its tile, or its Live tab. They stop 10 seconds after the last viewer
  leaves, and after 5 minutes whatever happens.
- The streaming port and RTSP refuse its streams, so a recorder cannot hold it awake.
- Its tile shows the last picture from the last time it was awake, and its battery level,
  read from the UDP search without waking it.
- Motion: live, with nothing to set up. When the camera wakes for motion, reostream wakes
  its stream within about a second, so the presence stream carries the motion live, and
  reports it (`reostream/<camera>/motion` on MQTT). See [Motion](#motion) below.
- Listing `streams` on a battery camera streams it all the time. Only do that on one with
  constant power.

### Battery cameras in Frigate

`rtsp://host:8561/<camera>` is a battery camera's presence stream: its last picture once a
second, each motion clip played in as it arrives, and live video while someone watches it
on the control page or while the camera sees motion. It never wakes the camera, so a recorder can hold it open around the
clock. Point Frigate at it with detection off, and add `-fpsprobesize 0` to the input:
at one frame a second, ffmpeg otherwise spends 20 seconds measuring the frame rate before
it outputs anything, Frigate gives up at 20 seconds, and the camera never records.

```yaml
go2rtc:
  streams:
    backyard: rtsp://<reostream-IP>:8561/backyard
cameras:
  backyard:
    ffmpeg:
      inputs:
        - path: rtsp://127.0.0.1:8554/backyard
          input_args: -rtsp_transport tcp -timeout 10000000 -fpsprobesize 0
          roles: [detect, record]
    detect:
      enabled: false
    record:
      continuous:
        days: 7
```

#### Motion

Live motion works with nothing to set up. reostream asks the camera once a second whether it
is asleep; the camera's low-power chip answers without waking it. When the camera's motion
sensor wakes it to record, reostream sees it within about a second and:

- wakes the stream for 30 seconds, so the presence stream, and Frigate recording it, carries
  the motion live instead of the last picture
- reports it like a wired camera's alerts: `<prefix>/<camera>/motion` on MQTT, and a Frigate
  event with `frigate_motion = true` (both need an `[events]` section)

The camera only needs motion recording on, as it is out of the box. Opening the camera in
the Reolink app wakes it too, and that looks the same from the network, so it also counts as
motion. reostream's own sessions (a viewer, the camera page) never do.

To keep the clips themselves, turn on **Auto-save motion clips** on the camera's page. The
camera then uploads each motion clip to reostream, which saves it on your disk and plays it
into the presence stream. A clip arrives some seconds after the motion starts, once the
camera has finished recording it.

#### Always on (constant power only)

A battery camera on constant power (a plug-in adapter, not solar) can stream around the
clock like a wired camera. Add `always_on = true` to it: reostream keeps it awake with nobody
watching and no 5-minute limit, so the presence stream is live all the time.

```toml
[[camera]]
name = "backyard"
uid = "9527000N031XXXXX"
always_on = true
```

Off by default. On a battery, even with solar, it drains the camera in days.

