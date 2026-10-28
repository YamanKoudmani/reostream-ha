# ReoStream for Home Assistant

This repository packages [ReoStream](https://github.com/VoltTech21/reostream) as a
Home Assistant app. ReoStream talks to Reolink cameras over their own Baichuan
protocol and serves clean RTSP and HTTP MPEG-TS streams, which avoids the broken
RTSP output on some Reolink models.

The app runs with host networking so it can find cameras on your LAN, receive
clips from them over FTP, and expose its streams directly:

| Port | Purpose |
|---|---|
| 8560 | HTTP MPEG-TS, snapshots, status API, Prometheus metrics |
| 8561 | RTSP for Frigate, go2rtc, Home Assistant or an NVR |
| 8562 | ReoStream web control panel |
| 2121-2131 | Optional FTP receiver for camera-uploaded clips |

## Install

1. In Home Assistant, open **Settings → Apps → Install app** (or **Add-on Store**
   on older versions).
2. Open the **⋮** menu, choose **Repositories**, and add:

   ```
   https://github.com/YamanKoudmani/reostream-ha
   ```

3. Install **ReoStream**, then start it.
4. Open **ReoStream** from the app page. The first start prints the generated
   control-panel password in the app log. You can set your own password under the
   app's **Configuration** tab before the first start.
5. Use **Add camera** in the ReoStream UI to find and add your cameras.

Full instructions, Home Assistant examples and troubleshooting are in the
[app documentation](reostream/DOCS.md).

## Source and licence

ReoStream is Copyright (c) 2026 Riley Poe and is licensed under the GNU Affero
General Public License v3. The upstream project is
[VoltTech21/reostream](https://github.com/VoltTech21/reostream). This repository
keeps the upstream source in [`reostream/`](reostream/) and adds the Home
Assistant packaging; the complete corresponding source is published here.
