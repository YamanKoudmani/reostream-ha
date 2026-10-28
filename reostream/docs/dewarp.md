# Dewarping a fisheye

## What the camera does and does not do

The camera sends a circle. It does not dewarp anything for a client, and no
message asks it to.

This was measured rather than assumed, because the code originally assumed
the opposite. While the phone app was drawing a wide defished panorama, the
camera reported `imageType 0` and a raw 2560x2560 circle. Switching the
app's display mode wrote nothing back: a diff of all 44 readable config
blocks across the change moved the clock and the CPU gauge and nothing else.

There are three separate things, and conflating them wasted a lot of time:

| Thing | Where it lives | Touches the stream? |
| --- | --- | --- |
| **Image Layout** (`FishEye.imageType`) | the camera | yes -- re-encodes, changes frame size, reboots |
| **Mount** (ceiling / wall / desktop) | the viewing client | no |
| **Display Modes** (fisheye, defished, dual panoramic, quad, cylindrical) | the viewing client | no |

`installType` in `fisheyeCfg` is a camera-side field, but the mount selector
in the app's Display Modes sheet is NOT that field -- changing it wrote
nothing to the camera.

### Image Layout values

Confirmed by reading `imageType` back while the vendor's client showed the
named mode:

- `0` Fisheye View, frame 2560x2560
- `1` Quad + 1 View, frame 3200x3200

`2` is almost certainly Dual Panoramic View -- the camera's menu lists
exactly three entries -- but has not been set and read back. The camera's
`range` reports a maximum of 3, which would be four values, while its
Support block reports three modes and its menu shows three. Something
over-reports; until that is settled `3` is offered without a name.

## How the vendor's own clients do it

The Windows client is an Electron app with an unpacked `resources/app`, and
its native SDK (`libBCSDKWrapper.dll`) carries the implementation. MSVC RTTI
names give the whole design away:

    DisplayModel                 base
    +- CommonDisplayModel        non-fisheye cameras
    +- FishOriginDisplayModel    Fisheye View
    +- ExpandDisplayModel        Defished View
    +- DualDisplayModel          Dual Panoramic View
    +- QuadDisplayModel          Quad View
    +- CylinderDisplayModel      Cylindrical View

One class per view mode, and each builds a **textured 3D mesh**. The vertex
shader is a plain world/view/projection transform and the pixel shader does
nothing but convert YUV to RGB -- `Texture2D texY/texU/texV : register(t0/t1/t2)`.
All the dewarp lives in where the vertices and their UVs are placed.

The important consequence: **dragging moves the view matrix, not the mesh**.
Quad View is one mesh with four cameras pointed into it, rendered to four
viewports. Nothing regenerates and nothing round-trips to the camera.

`fishEyeSubChnCtrl` (message 541) is real but is a different feature. It has
nothing to do with these display modes.

## How we do it

`internal/control/assets/fisheye.js`. The geometry is in the fragment shader
instead of a mesh: one screen-filling triangle pair, and every output pixel
becomes a ray, whose angle from the lens axis picks a radius on the circle.

    theta = acos(ray.z)                  angle from the lens axis
    r     = theta / halfFov              equidistant lens
    uv    = center + dir * r * radius    dir is the ray's bearing

Fewer moving parts than a mesh, one draw call per pane, and no quantizing
onto vertices.

Two traps, both of which shipped broken once:

- **Texture v runs down.** `v = 0` is the first row uploaded, the top of the
  frame, while the ray's y runs up. They are reconciled in exactly one place
  (negating y as the bearing is built). Fixing it by flipping the upload
  instead would silently invert the ceiling/wall mount flip, which also
  works on `r.y`.
- **Self-consistent is not correct.** Eight numeric projection checks passed
  while the whole picture was mirrored about the horizontal axis. Anything
  asserting orientation has to be anchored to a real frame.

## Projections

Because the shader maps a ray to a pixel, a new projection is a different
ray function -- a few lines of GLSL, not a new mesh. That is the advantage
over the vendor's approach, and it is why we are not limited to their five.

Currently implemented: **rectilinear** (perspective), **stereographic**, and
the **panoramic band**, any of them in 1, 2 or 4 panes.

### Stereographic, as built

Two lines of the shader and one uniform. The projection decides only the
angle out from the pane's center; the bearing around that center is the
same for every projection, which is why the branch is that small:

    rectilinear    plane radius = tan(theta)        inverse: atan(q*tan(z))
    stereographic  plane radius = 2*tan(theta/2)    inverse: 2*atan(q*tan(z/2))

Both are scaled so that the pane's vertical edge sits at exactly the zoom
half-angle, whichever is chosen. That is what makes switching projection
show the same scene drawn differently rather than jumping the zoom, and it
is why `setProjection` keeps each pane's aim and width untouched.

Three numbers in the code are geometry rather than taste, and are worth not
re-tuning by eye:

- **Reset opens a stereographic pane to 0.9 rad.** A 16:9 pane's corner is
  2.04 times its half-height out from the center, and stereographic puts
  that corner at `2*atan(2.04*tan(z/2))`, which reaches 90 degrees off axis
  at z = 0.911. Wider is allowed and sometimes wanted -- it is the whole
  hemisphere, vignetted into a circle -- but a reset should not hand you
  black corners.
- **A stereographic pane may open to the lens's own half-FOV**, not to
  rectilinear's 1.35. No projection can show more than the lens saw.
- **The fan-out tilt for 2 and 4 panes is `halfFov - defaultZoom`**, not the
  constant 1.0 it used to be. 1.0 was that answer for a 0.6 rad pane; with a
  0.9 rad pane the same constant opens a quad view with a black band across
  the top of all four.

Measured on a real frame from the fisheye, not on numbers alone -- the same
file's eight self-consistent checks once passed on a mirrored picture:

- a 2560x2560 frame was pulled off the live stream with ffmpeg (the camera
  was not touched for this at all), dewarped by a second, independent
  implementation of the shader written in plain JavaScript, and looked at.
  Text on the far wall reads left to right, the ceiling is at the top.
- the shipped shader was then run in a real browser against that same frame
  and gives the same picture, which is what says the GLSL compiles and that
  the two implementations agree.
- rectilinear came out unchanged: the old and new ray functions differ by at
  most 3e-8 radians across five zooms and 40,401 sample points, which is
  float32 noise. The two browser renders differ only in nearest-texel
  rounding on high-contrast edges (mean 0.025/255, max 9/255).

At the same width, rectilinear keeps the straight lines straight and cuts
off both side walls; stereographic shows both side walls, the full desk and
the ceiling, and bends the long lines. That is the whole trade and it is
visible immediately on a real room.

### The panoramic band, and what it settled about the mount

The band is not a pinhole view at all: the two screen axes ARE the two
angles. Across is the way round an axis, up and down is the angle out from
it, so the ring the lens saw unrolls into a strip. Its zoom is therefore
half an azimuth SPAN rather than half a field of view, and the vertical span
is derived from it (`zoom / aspect`) so the degrees per pixel come out the
same both ways and the strip letterboxes rather than stretches.

The panes of a layout split the sweep between them instead of each showing
all of it, so two panes is exactly the vendor's Dual Panoramic and four is
the same idea in quarters. One pane is the whole sweep.

**Which axis it unrolls around is the mount**, and that is where this got
interesting. A ceiling camera's lens axis points down the middle of the
room, so the ring around it is the horizon and unrolling it gives a
panorama. A camera on a wall looks along the room: the ring around ITS lens
axis is a vertical hoop, and unrolling that stands the room on its side --
which is exactly what the first working version drew, because it only had
the ceiling case. A wall band has to run around the room's upright, which is
the picture's own up: the lens frame's y, not its z. Two more details that
each cost a render to find:

- the wall band's phi must START on the lens axis, because the hemisphere it
  saw is the half in front of the camera. Starting it a quarter turn off
  leaves half the strip black, which is what the second render showed.
- up the screen is away from whichever axis the strip is unrolled around,
  and those point opposite ways in the two mounts, so the vertical sense has
  to flip with the mount too. Otherwise the panorama is upside down while
  every number in it still checks out.

With all three right, a 180 degree wall band of this room comes out as a
proper panorama: text readable, door frames vertical, ceiling at the top,
both side walls and the desk in one strip.

And it settled the open question from the stereographic work. The band's
wall case is derived from first principles and produces a correct picture
with the Mounted selector on **Wall** -- so the camera IS a wall mount and
the selector was right. The radial projections, on the same camera and the
same frame, needed **Ceiling** to come out upright. Both cannot be right:
the radial mirror was simply the wrong way round.

So `flip` is no longer the mount. `setMount` is, and it sets two things from
one answer: which axis a band unrolls around, and whether the picture is
mirrored -- a lens pointing DOWN hands back the opposite handedness to one
pointing along the room, so the ceiling case mirrors and the wall case does
not. The wall half of that is measured on a real frame. **The ceiling half
is the mirror of it and has never been seen on a ceiling camera**, which is
worth an operator's eye before anyone calls it confirmed.

The page's mount values are arguments, not labels -- the select's string
goes straight to `setMount` -- so a test holds the two option values and the
two the renderer names together. Renaming one would otherwise turn every
ceiling camera's picture upside down with no error anywhere.

Worth having next, roughly in order of usefulness for a shop:

- **Equirectangular.** The whole hemisphere in one rectangle. Ugly at the
  edges but nothing is hidden, which makes it a good recording projection.
- **Panini.** Wide like stereographic but keeps verticals straight, which
  flatters interiors with door frames and shelving.
- **Little planet (stereographic, inverted).** Novelty, cheap to add once
  stereographic exists.

Things the vendor cannot do that fall out of this design for free:

- several projections on screen at once
- an arbitrary pane count, not just 1/2/4
- a different projection per pane -- one zoomed rectilinear beside one full
  panoramic band
- saved named presets per pane ("register", "bay 2", "front door")
- lens field of view beyond 180, if a lens ever provides it

## Making a dewarped STREAM (not built)

The browser page cannot serve the cam wall or Frigate, which want a plain
URL. That needs a real transcode: decode, warp, re-encode.

**This does not belong inside reostream.** The daemon never decodes a frame
today -- camera, Baichuan, remux, MPEG-TS -- and that is why it is one static
binary with no ffmpeg dependency and why eight cameras fit on the box. A
sidecar keeps that intact and keeps the recorder out of harm's way.

The natural URL is the **`extern` slot**. The fisheye has main and sub and no
extern, and every consumer already understands `<cam>_extern.ts` -- Frigate
configs, the cam wall, `externStream()` in `internal/control/tiles.go`. A
dewarped output landing there needs no new concept anywhere downstream.

What is available on the test host (measured 2026-09-25):

    v360            Convert 360 projection of video   .SC  (C = runtime commands)
    remap_opencl    Remap pixels using OpenCL
    GPU             Intel Arc A310 (DG2), /dev/dri/renderD128
    encoders        h264_qsv, hevc_qsv, av1_qsv, *_vaapi, *_nvenc
    CPU             Xeon E5-1650 (6c/12t, 2012), load ~5.6 of 12

`v360` does fisheye to flat, cylindrical or panoramic natively and accepts
runtime yaw/pitch commands. **But it is CPU-only** -- no QSV or VAAPI
version. The naive pipeline decodes on the Arc, drags 2560x2560 frames back
to system memory at 25fps, warps on a 2012 Xeon already at half load, and
uploads again. The cheap path is `remap_opencl` with maps precomputed once
per view, keeping decode, warp and encode all on the Arc. The hardware is
already adequate; a separate machine is a fallback, not a prerequisite.

Design points that are decisions, not details:

- **A stream has one fixed aim.** Panning is per-viewer by nature. On a
  shared stream one person panning moves it for every viewer and every
  recorder. Fixed named views are probably what is wanted, not a draggable
  one.
- **Dewarping discards information.** A pane is a crop of the circle.
  Recording should keep the raw stream; dewarped outputs are for viewing.
  A dewarped recording permanently loses whatever sat outside the pane.
- **One decode, many views.** Several outputs should come from a single
  decode via a `split` filter with N warps in one graph, not N ffmpeg
  processes.

Not yet measured: the actual GPU and CPU cost of either path. That needs a
60-second run on the live box.
