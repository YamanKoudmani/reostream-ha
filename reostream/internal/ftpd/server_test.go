package ftpd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A client doing what the Argus's FTP client does: log in, make and enter
// the dated folder, upload in passive mode. Plus the two refusals that
// matter: a wrong password, and a path that tries to leave the folder.
func TestACameraUploadLandsInItsOwnFolder(t *testing.T) {
	root := t.TempDir()
	got := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Start(ctx, Config{
		Listen: "127.0.0.1:0", Root: root, PassiveMin: 41820, PassiveMax: 41829,
		Auth: func(u, p string) (string, bool) { return u, u == "argus" && p == "pw" },
		Stored: func(folder, file string, size int64) {
			rel, _ := filepath.Rel(root, file)
			got <- fmt.Sprintf("%s %s %d", folder, filepath.ToSlash(rel), size)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(cmd string, want string) string {
		t.Helper()
		if cmd != "" {
			fmt.Fprintf(c, "%s\r\n", cmd)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(line, want) {
			t.Fatalf("%q answered %q, want %s", cmd, line, want)
		}
		return line
	}
	say("", "220")
	say("USER argus", "331")
	say("PASS nope", "530")
	say("USER argus", "331")
	say("PASS pw", "230")
	say("MKD argus/2026/10/22", "257")
	say("CWD argus/2026/10/22", "250")
	say("TYPE I", "200")
	line := say("PASV", "227")
	nums := strings.Split(line[strings.Index(line, "(")+1:strings.Index(line, ")")], ",")
	hi, _ := strconv.Atoi(nums[4])
	lo, _ := strconv.Atoi(nums[5])
	fmt.Fprintf(c, "STOR Battery Cam_00_20261022152202.mp4\r\n")
	dc, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hi<<8|lo))
	if err != nil {
		t.Fatal(err)
	}
	say("", "150")
	io.WriteString(dc, "clip-bytes")
	dc.Close()
	say("", "226")
	if g := <-got; g != "argus argus/argus/2026/10/22/Battery Cam_00_20261022152202.mp4 10" {
		t.Fatalf("stored %q", g)
	}
	// ".." cannot climb out of the folder: it lands inside it.
	say("CWD /", "250")
	say("MKD ../../escape", "257")
	if _, err := os.Stat(filepath.Join(root, "escape")); err == nil {
		t.Fatal("a path escaped the login's folder")
	}
	if _, err := os.Stat(filepath.Join(root, "argus", "escape")); err != nil {
		t.Fatal("the cleaned path was not created inside the folder")
	}
	say("QUIT", "221")
}

// A disk with less than MinFree left refuses the upload (452) and says so,
// rather than failing partway through the file.
func TestUploadRefusedWhenTheDiskIsNearlyFull(t *testing.T) {
	root := t.TempDir()
	free, ok := freeBytes(root)
	if !ok {
		t.Skip("no free-space reading here")
	}
	var told bool
	var out bytes.Buffer
	ss := &session{s: &Server{cfg: Config{Root: root, MinFree: free + 1<<40, Full: func(int64) { told = true }}}, w: bufio.NewWriter(&out)}
	ss.store("clip.mp4", false)
	if !told || !strings.HasPrefix(out.String(), "452") {
		t.Fatalf("told=%v reply=%q", told, out.String())
	}
}
