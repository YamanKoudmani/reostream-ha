module github.com/VoltTech21/reostream

go 1.26.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/bluenviron/gortsplib/v5 v5.6.5
	github.com/bluenviron/mediacommon/v2 v2.9.4
	github.com/gorilla/websocket v1.5.3
	github.com/pion/rtp v1.10.5
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.17 // indirect
	github.com/pion/sdp/v3 v3.0.19 // indirect
	github.com/pion/srtp/v3 v3.0.13 // indirect
	github.com/pion/transport/v4 v4.1.0 // indirect
)

replace github.com/bluenviron/gortsplib/v5 => ./third_party/gortsplib
