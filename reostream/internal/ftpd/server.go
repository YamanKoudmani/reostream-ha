// Package ftpd is the small FTP server cameras upload their clips to. It
// implements what a camera's FTP client uses -- login, directories,
// passive and active data connections, STOR -- and keeps every login inside
// its own folder. It accepts uploads and lists what it holds; it serves no
// downloads.
package ftpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is how the server listens and where files go.
type Config struct {
	// Listen is the control address, e.g. ":2121".
	Listen string
	// Root holds one folder per login.
	Root string
	// PassiveMin and PassiveMax bound the data ports offered for PASV.
	PassiveMin, PassiveMax int
	// PublicHost is the address told to clients for passive data; empty
	// uses the address the client reached the control connection on.
	PublicHost string
	// Auth checks a login and returns the folder name for it.
	Auth func(user, pass string) (folder string, ok bool)
	// MaxFile caps one upload; 0 means 4 GiB.
	MaxFile int64
	// Stored is told about each file once it is fully received.
	Stored func(folder, file string, size int64)
	// MinFree refuses uploads while Root's disk has less than this many
	// bytes free; 0 is no limit. Full is told when one is refused.
	MinFree int64
	Full    func(free int64)
}

// Server is a running FTP server.
type Server struct {
	cfg  Config
	ln   net.Listener
	wg   sync.WaitGroup
	next int
	mu   sync.Mutex
}

// Start listens and serves until ctx ends.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.MaxFile == 0 {
		cfg.MaxFile = 4 << 30
	}
	if cfg.PassiveMin == 0 {
		cfg.PassiveMin, cfg.PassiveMax = 2122, 2131
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, ln: ln, next: cfg.PassiveMin}
	go func() { <-ctx.Done(); ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.serve(c) }()
		}
	}()
	return s, nil
}

// Addr is the control listener's address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

type session struct {
	s      *Server
	c      net.Conn
	w      *bufio.Writer
	user   string
	folder string // empty until logged in
	cwd    string // slash path inside the folder, always starting "/"
	pasv   net.Listener
	port   string // active mode target
	rnfr   string
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	ss := &session{s: s, c: c, w: bufio.NewWriter(c), cwd: "/"}
	defer ss.closeData()
	ss.reply(220, "reostream ready")
	r := bufio.NewReader(c)
	for {
		c.SetReadDeadline(time.Now().Add(5 * time.Minute))
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		if !ss.handle(strings.ToUpper(cmd), arg) {
			return
		}
	}
}

func (ss *session) reply(code int, msg string) {
	fmt.Fprintf(ss.w, "%d %s\r\n", code, msg)
	ss.w.Flush()
}

// local turns a client path into a file path inside the login's folder.
// path.Clean of a rooted path cannot climb above "/", so nothing outside
// the folder is reachable.
func (ss *session) local(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(ss.cwd, p)
	}
	p = path.Clean("/" + p)
	return filepath.Join(ss.s.cfg.Root, ss.folder, filepath.FromSlash(p))
}

func (ss *session) virtual(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(ss.cwd, p)
	}
	return path.Clean("/" + p)
}

func (ss *session) handle(cmd, arg string) bool {
	switch cmd {
	case "USER":
		ss.user, ss.folder = arg, ""
		ss.reply(331, "password please")
		return true
	case "PASS":
		if ss.s.cfg.Auth == nil {
			ss.reply(530, "no logins")
			return true
		}
		folder, ok := ss.s.cfg.Auth(ss.user, arg)
		if !ok || folder == "" || strings.ContainsAny(folder, `/\`) || folder == ".." {
			ss.reply(530, "login incorrect")
			return true
		}
		ss.folder = folder
		os.MkdirAll(filepath.Join(ss.s.cfg.Root, folder), 0o755)
		ss.reply(230, "logged in")
		return true
	case "QUIT":
		ss.reply(221, "bye")
		return false
	case "NOOP":
		ss.reply(200, "ok")
		return true
	case "SYST":
		ss.reply(215, "UNIX Type: L8")
		return true
	case "FEAT":
		fmt.Fprintf(ss.w, "211-Features:\r\n EPSV\r\n PASV\r\n SIZE\r\n UTF8\r\n211 End\r\n")
		ss.w.Flush()
		return true
	case "OPTS", "TYPE", "MODE", "STRU", "ALLO":
		ss.reply(200, "ok")
		return true
	}
	if ss.folder == "" {
		ss.reply(530, "log in first")
		return true
	}
	switch cmd {
	case "PWD", "XPWD":
		ss.reply(257, strconv.Quote(ss.cwd)+" is the current directory")
	case "CWD", "XCWD":
		v := ss.virtual(arg)
		if st, err := os.Stat(ss.local(v)); err != nil || !st.IsDir() {
			ss.reply(550, "no such directory")
			break
		}
		ss.cwd = v
		ss.reply(250, "ok")
	case "CDUP", "XCUP":
		ss.cwd = path.Dir(ss.cwd)
		ss.reply(250, "ok")
	case "MKD", "XMKD":
		if err := os.MkdirAll(ss.local(arg), 0o755); err != nil {
			ss.reply(550, "cannot create")
			break
		}
		ss.reply(257, strconv.Quote(ss.virtual(arg))+" created")
	case "SIZE":
		if st, err := os.Stat(ss.local(arg)); err == nil && !st.IsDir() {
			ss.reply(213, strconv.FormatInt(st.Size(), 10))
		} else {
			ss.reply(550, "no such file")
		}
	case "DELE":
		if err := os.Remove(ss.local(arg)); err != nil {
			ss.reply(550, "cannot delete")
		} else {
			ss.reply(250, "deleted")
		}
	case "RNFR":
		ss.rnfr = ss.local(arg)
		ss.reply(350, "ready")
	case "RNTO":
		if ss.rnfr == "" || os.Rename(ss.rnfr, ss.local(arg)) != nil {
			ss.reply(550, "cannot rename")
		} else {
			ss.reply(250, "renamed")
			ss.stored(ss.local(arg))
		}
		ss.rnfr = ""
	case "PASV", "EPSV":
		ss.passive(cmd == "EPSV")
	case "PORT":
		ss.active(arg)
	case "EPRT":
		ss.activeExtended(arg)
	case "STOR", "APPE":
		ss.store(arg, cmd == "APPE")
	case "LIST", "NLST":
		ss.list(cmd == "NLST")
	case "REST":
		if arg == "0" {
			ss.reply(350, "ok")
		} else {
			ss.reply(502, "resume not supported")
		}
	default:
		ss.reply(502, "not implemented")
	}
	return true
}

func (ss *session) closeData() {
	if ss.pasv != nil {
		ss.pasv.Close()
		ss.pasv = nil
	}
	ss.port = ""
}

func (ss *session) passive(extended bool) {
	ss.closeData()
	cfg := ss.s.cfg
	var ln net.Listener
	for i := 0; i <= cfg.PassiveMax-cfg.PassiveMin; i++ {
		ss.s.mu.Lock()
		p := ss.s.next
		ss.s.next++
		if ss.s.next > cfg.PassiveMax {
			ss.s.next = cfg.PassiveMin
		}
		ss.s.mu.Unlock()
		l, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err == nil {
			ln = l
			break
		}
	}
	if ln == nil {
		ss.reply(425, "no data port free")
		return
	}
	ss.pasv = ln
	port := ln.Addr().(*net.TCPAddr).Port
	if extended {
		ss.reply(229, fmt.Sprintf("Entering extended passive mode (|||%d|)", port))
		return
	}
	host := cfg.PublicHost
	if host == "" {
		host, _, _ = net.SplitHostPort(ss.c.LocalAddr().String())
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		ss.reply(425, "passive needs an IPv4 address")
		return
	}
	ss.reply(227, fmt.Sprintf("Entering passive mode (%d,%d,%d,%d,%d,%d)", ip[0], ip[1], ip[2], ip[3], port>>8, port&255))
}

func (ss *session) active(arg string) {
	ss.closeData()
	p := strings.Split(arg, ",")
	if len(p) != 6 {
		ss.reply(501, "bad PORT")
		return
	}
	hi, _ := strconv.Atoi(p[4])
	lo, _ := strconv.Atoi(p[5])
	ss.port = net.JoinHostPort(strings.Join(p[:4], "."), strconv.Itoa(hi<<8|lo))
	ss.reply(200, "ok")
}

func (ss *session) activeExtended(arg string) {
	ss.closeData()
	if len(arg) < 2 {
		ss.reply(501, "bad EPRT")
		return
	}
	f := strings.Split(arg[1:len(arg)-1], string(arg[0]))
	if len(f) != 3 {
		ss.reply(501, "bad EPRT")
		return
	}
	ss.port = net.JoinHostPort(f[1], f[2])
	ss.reply(200, "ok")
}

// data opens the data connection the client asked for.
func (ss *session) data() (net.Conn, error) {
	defer ss.closeData()
	if ss.pasv != nil {
		ss.pasv.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second))
		return ss.pasv.Accept()
	}
	if ss.port != "" {
		return net.DialTimeout("tcp", ss.port, 15*time.Second)
	}
	return nil, errors.New("no data connection")
}

func (ss *session) store(name string, appendTo bool) {
	if name == "" {
		ss.reply(501, "no file name")
		return
	}
	if min := ss.s.cfg.MinFree; min > 0 {
		if free, ok := freeBytes(ss.s.cfg.Root); ok && free < min {
			if ss.s.cfg.Full != nil {
				ss.s.cfg.Full(free)
			}
			ss.reply(452, "not enough space")
			return
		}
	}
	dst := ss.local(name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		ss.reply(550, "cannot create folder")
		return
	}
	ss.reply(150, "send it")
	dc, err := ss.data()
	if err != nil {
		ss.reply(425, "no data connection")
		return
	}
	defer dc.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(dst, flags, 0o644)
	if err != nil {
		ss.reply(550, "cannot write")
		return
	}
	dc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	n, err := io.Copy(f, io.LimitReader(dc, ss.s.cfg.MaxFile+1))
	cerr := f.Close()
	if err != nil || cerr != nil || n > ss.s.cfg.MaxFile {
		os.Remove(dst)
		ss.reply(451, "upload failed")
		return
	}
	ss.reply(226, "stored")
	ss.stored(dst)
}

func (ss *session) stored(file string) {
	st, err := os.Stat(file)
	if err != nil || st.IsDir() {
		return
	}
	if ss.s.cfg.Stored != nil {
		ss.s.cfg.Stored(ss.folder, file, st.Size())
	} else {
		log.Printf("ftpd: %s stored %s (%d bytes)", ss.folder, filepath.Base(file), st.Size())
	}
}

func (ss *session) list(namesOnly bool) {
	entries, _ := os.ReadDir(ss.local(ss.cwd))
	ss.reply(150, "listing")
	dc, err := ss.data()
	if err != nil {
		ss.reply(425, "no data connection")
		return
	}
	for _, e := range entries {
		if namesOnly {
			fmt.Fprintf(dc, "%s\r\n", e.Name())
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "-rw-r--r--"
		if e.IsDir() {
			kind = "drwxr-xr-x"
		}
		fmt.Fprintf(dc, "%s 1 cam cam %d %s %s\r\n", kind, info.Size(), info.ModTime().Format("Jan _2 15:04"), e.Name())
	}
	dc.Close()
	ss.reply(226, "done")
}

// FreeBytes is the space left on dir's disk, for a page to show.
func FreeBytes(dir string) (int64, bool) { return freeBytes(dir) }
