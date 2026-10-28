# Patched gortsplib

A copy of github.com/bluenviron/gortsplib/v5 v5.6.5 (MIT, see LICENSE), used through
a `replace` in reostream's go.mod. Tests and examples are left out.

Two small changes in `server_session.go` (search "reostream patch"): a repeated SETUP of a media the session already
has, over TCP, is answered with the transport it already got instead of 400.

go2rtc sends a repeated SETUP for the ONVIF back channel when two of its consumers
start two-way audio at once. On the 400 it turns the back channel off for that
connection until it restarts, so Frigate's talk button stops working.
