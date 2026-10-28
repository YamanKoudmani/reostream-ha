package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Network services: the camera's own RTSP / ONVIF / RTMP / HTTP / HTTPS
// servers, from the ServerPort document (messages 37 read, 36 write; see
// baichuan/netport.go). reostream itself needs none of them -- it talks to
// the camera on the media port, 9000 -- but a recorder or the camera's own
// web page might, so each can be switched here. Ports are shown, not
// edited.

type netService struct {
	Key, Label, Help string
	Port             int
	On               bool
}

type netPortView struct {
	Have      bool
	Err       string
	Services  []netService
	MediaPort int
}

// indexOf is a service key's position in netServices.
func indexOf(key string) int {
	for i, k := range []string{"rtsp", "onvif", "rtmp", "http", "https"} {
		if k == key {
			return i
		}
	}
	return -1
}

func netServices(np baichuan.NetPort) []netService {
	return []netService{
		{"rtsp", "RTSP", "Lets recorders read the camera directly.", np.RTSP.Port, np.RTSP.Enabled},
		{"onvif", "ONVIF", "Discovery and PTZ for ONVIF recorders.", np.ONVIF.Port, np.ONVIF.Enabled},
		{"rtmp", "RTMP", "Older streaming; rarely needed.", np.RTMP.Port, np.RTMP.Enabled},
		{"http", "HTTP", "The camera's own web page.", np.HTTP.Port, np.HTTP.Enabled},
		{"https", "HTTPS", "The camera's own web page, encrypted.", np.HTTPS.Port, np.HTTPS.Enabled},
	}
}

func (s *Server) readNetPort(ctx context.Context, cam Camera) netPortView {
	conn, err := s.dial(ctx, cam)
	if err != nil {
		return netPortView{Err: fmt.Sprintf("could not connect: %v", err)}
	}
	defer conn.Close()
	np, err := conn.GetNetPort(ctx)
	if err != nil {
		return netPortView{Err: fmt.Sprintf("could not read the network services: %v", err)}
	}
	return netPortView{Have: true, Services: netServices(np), MediaPort: np.ServerPort.Port}
}

// serveApplyNetPort switches one service on or off, or moves its port.
func (s *Server) serveApplyNetPort(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byNameDirect(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	key := r.FormValue("service")
	back := "/cameras/" + url.PathEscape(cam.Name) + "/settings/network"

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": could not connect: " + err.Error()})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	defer conn.Close()
	np, err := conn.GetNetPort(ctx)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": could not read the network services: " + err.Error()})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	want := np
	var svc *baichuan.PortService
	label := ""
	switch key {
	case "rtsp":
		svc, label = &want.RTSP, "RTSP"
	case "onvif":
		svc, label = &want.ONVIF, "ONVIF"
	case "rtmp":
		svc, label = &want.RTMP, "RTMP"
	case "http":
		svc, label = &want.HTTP, "HTTP"
	case "https":
		svc, label = &want.HTTPS, "HTTPS"
	default:
		http.Error(w, "unknown service", http.StatusBadRequest)
		return
	}
	what := ""
	if v := r.FormValue("value"); v != "" {
		svc.Enabled = v == "1"
		what = "off"
		if svc.Enabled {
			what = "on"
		}
	}
	if v := r.FormValue("port"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			s.setFlash(w, r, Flash{Outcome: "refused", Message: fmt.Sprintf("%s: %q is not a port", label, v)})
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		svc.Port = p
		what = "on port " + v
	}
	if err := conn.SetPorts(ctx, want); err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: fmt.Sprintf("%s: %s could not be changed: %v", cam.Name, label, err)})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	// Read it back: the ack says the camera took the document, not that the
	// service changed.
	after, err := conn.GetNetPort(ctx)
	if err != nil || netServices(after)[indexOf(key)] != netServices(want)[indexOf(key)] {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: fmt.Sprintf("%s: the camera accepted %s but still reports it otherwise", cam.Name, label)})
	} else {
		s.setFlash(w, r, Flash{Outcome: "confirmed", Message: fmt.Sprintf("%s: %s %s", cam.Name, label, what)})
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
