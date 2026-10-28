// Command reoevent turns a battery camera's cloud event reports into local
// MQTT. It passively sniffs the camera's cf31 traffic on an interface it
// shares a path with (a router, a bridge, or a mirrored port), decodes the
// motion, recording and status events, and republishes them to an MQTT broker
// for Home Assistant, Frigate or anything else on the LAN.
//
// This is the instant local-motion path: the camera sends its motion event
// the moment it wakes, so MQTT fires in well under a second — far ahead of an
// FTP upload or a finished SD recording. It needs no cloud account and never
// touches the camera; it only listens.
//
// It requires CAP_NET_RAW (usually root) to open the capture socket.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VoltTech21/reostream/internal/bcevent"
	"github.com/VoltTech21/reostream/internal/mqtt"
)

func main() {
	iface := flag.String("iface", "", "interface to capture on (required), e.g. enp1s0 or br-lan")
	broker := flag.String("broker", "127.0.0.1:1883", "MQTT broker host:port; empty to only log events")
	user := flag.String("mqtt-user", "", "MQTT username")
	pass := flag.String("mqtt-pass", "", "MQTT password")
	clientID := flag.String("mqtt-client", "reoevent", "MQTT client id")
	topic := flag.String("topic", "reostream", "MQTT topic prefix")
	name := flag.String("name", "", "camera name used in topics (default: the camera UID)")
	uid := flag.String("uid", "", "only report this camera UID (default: every camera seen)")
	motionTimeout := flag.Duration("motion-timeout", 20*time.Second, "publish motion OFF this long after the last motion")
	promisc := flag.Bool("promisc", true, "put the interface in promiscuous mode (needed on a bridge/router)")
	noBPF := flag.Bool("no-bpf", false, "diagnostic: skip the kernel filter, match in userspace")
	debug := flag.Bool("debug", false, "diagnostic: log a count of raw frames captured")
	qos := flag.Int("qos", 1, "MQTT QoS for motion/recording (0 or 1)")
	flag.Parse()

	if *iface == "" {
		fmt.Fprintln(os.Stderr, "reoevent: -iface is required")
		flag.Usage()
		os.Exit(2)
	}
	if *qos < 0 || *qos > 1 {
		log.Fatal("reoevent: -qos must be 0 or 1")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var pub publisher = logPublisher{}
	if *broker != "" {
		pub = &mqttPublisher{c: mqtt.New(*broker, *clientID, *user, *pass), qos: byte(*qos)}
		log.Printf("reoevent: publishing to mqtt://%s under %q", *broker, *topic)
	} else {
		log.Printf("reoevent: no broker, logging events only")
	}

	f := &feeder{
		pub:           pub,
		prefix:        *topic,
		name:          *name,
		uid:           *uid,
		motionTimeout: *motionTimeout,
	}

	// The sniffer calls into fn on its own goroutine; hand each event to the
	// feeder loop through a channel so one goroutine owns the motion timer.
	ch := make(chan bcevent.Report, 64)
	sniffer := &bcevent.Sniffer{Iface: *iface, Promisc: *promisc, NoFilter: *noBPF, Debug: *debug}
	go func() {
		err := sniffer.Run(ctx, func(p bcevent.Packet) {
			if *uid != "" && p.Event.UID != *uid {
				return
			}
			r, err := bcevent.Parse(p.Event.Body)
			if err != nil {
				log.Printf("reoevent: decode event from %s: %v", p.Src, err)
				return
			}
			f.uidSeen = p.Event.UID
			select {
			case ch <- r:
			default:
				log.Print("reoevent: event queue full, dropping")
			}
		})
		if err != nil {
			log.Printf("reoevent: capture stopped: %v", err)
		}
		stop()
	}()

	log.Printf("reoevent: listening on %s for cf31 events", *iface)
	f.run(ctx, ch)
}

// publisher is the sink for a camera's events; mqttPublisher sends them to a
// broker, logPublisher just prints them.
type publisher interface {
	publish(topic string, payload []byte, retain bool)
}

type mqttPublisher struct {
	c   *mqtt.Client
	qos byte
}

func (m *mqttPublisher) publish(topic string, payload []byte, retain bool) {
	if err := m.c.Publish(topic, payload, m.qos, retain); err != nil {
		log.Printf("reoevent: publish %s: %v", topic, err)
	}
}

type logPublisher struct{}

func (logPublisher) publish(topic string, payload []byte, retain bool) {
	log.Printf("reoevent: %s = %s", topic, payload)
}

type feeder struct {
	pub           publisher
	prefix        string
	name          string
	uid           string
	motionTimeout time.Duration
	uidSeen       string
}

func (f *feeder) camera() string {
	if f.name != "" {
		return f.name
	}
	if f.uidSeen != "" {
		return f.uidSeen
	}
	return "camera"
}

func (f *feeder) topic(leaf string) string {
	return f.prefix + "/" + f.camera() + "/" + leaf
}

func (f *feeder) run(ctx context.Context, ch <-chan bcevent.Report) {
	motionOff := time.NewTimer(time.Hour)
	if !motionOff.Stop() {
		<-motionOff.C
	}
	motionOn := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-motionOff.C:
			if motionOn {
				motionOn = false
				f.pub.publish(f.topic("motion"), []byte("OFF"), false)
			}
		case r := <-ch:
			f.handle(r, &motionOn, motionOff)
		}
	}
}

func (f *feeder) handle(r bcevent.Report, motionOn *bool, motionOff *time.Timer) {
	// The raw event is always republished, so a template or a yet-unmodeled
	// event type is still usable downstream.
	f.pub.publish(f.topic("event"), r.Raw, false)

	switch r.Kind {
	case bcevent.Motion:
		if !*motionOn {
			*motionOn = true
			f.pub.publish(f.topic("motion"), []byte("ON"), false)
		}
		resetTimer(motionOff, f.motionTimeout)
		log.Printf("reoevent: MOTION %s", f.camera())
	case bcevent.RecordingEnd:
		b, _ := json.Marshal(map[string]int{"duration_ms": r.DurationMS, "reason": r.Reason})
		f.pub.publish(f.topic("recording"), b, false)
		log.Printf("reoevent: recording ended %s (%.1fs, reason %d)", f.camera(), float64(r.DurationMS)/1000, r.Reason)
	case bcevent.Status:
		f.pub.publish(f.topic("status"), statusJSON(r), true)
	}
}

func statusJSON(r bcevent.Report) []byte {
	s := map[string]any{
		"awake":     r.Awake(),
		"wifi_rssi": r.WifiRSSI,
		"ts":        r.Time,
	}
	if b := r.Battery; b != nil {
		s["battery_pct"] = b.Pct
		s["battery_mv"] = b.Millivolts
		s["temp_c"] = b.TempC
		s["charging"] = b.Current > 0
		s["battery_low"] = b.Low != 0
	}
	out, _ := json.Marshal(s)
	return out
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
