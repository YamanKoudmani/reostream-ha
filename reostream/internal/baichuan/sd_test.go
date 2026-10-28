package baichuan

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Replies an Argus PT Ultra gave (2026-10-24) to the client's own requests.

func TestParseDayRecordsFromACamera(t *testing.T) {
	doc, err := os.ReadFile("testdata/sd-dayrecords.xml")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	days, err := parseDayRecords(doc, from)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Day() != 22 || days[1].Day() != 23 {
		t.Fatalf("days = %v, want the 22nd and 23rd", days)
	}
}

func TestParseFilesFromACamera(t *testing.T) {
	doc, err := os.ReadFile("testdata/sd-files.xml")
	if err != nil {
		t.Fatal(err)
	}
	handle, files, err := parseFiles(doc)
	if err != nil {
		t.Fatal(err)
	}
	if handle != 1 || len(files) == 0 {
		t.Fatalf("handle %d, %d files", handle, len(files))
	}
	f := files[0]
	if f.Name != "0120261023090421" || !strings.HasSuffix(f.Path, ".mp4") || !f.HasAudio || !f.HasSub {
		t.Fatalf("first file %+v", f)
	}
	if f.Start.Hour() != 9 || f.Start.Minute() != 4 || f.Start.Second() != 21 || f.End.Sub(f.Start) != 23*time.Second {
		t.Fatalf("times %v - %v", f.Start, f.End)
	}
	if f.Size != 3466436 || f.Type != "other" {
		t.Fatalf("size %d type %q", f.Size, f.Type)
	}
}

// The requests must match the client's byte for byte where the camera is
// strict: one document, these elements, this order.
func TestSDRequestsMatchTheClient(t *testing.T) {
	from := time.Date(2026, 10, 22, 14, 25, 32, 0, time.Local)
	to := time.Date(2026, 10, 22, 14, 25, 50, 0, time.Local)
	got := string(downloadDoc(0, false, from, to))
	want := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<FileInfoList version=\"1.1\">\n<FileInfo>\n<logicChnBitmap>255</logicChnBitmap>\n<channelId>0</channelId>\n<supportSub>0</supportSub>\n<streamType>mainStream</streamType>\n<startTime>\n<year>2026</year>\n<month>10</month>\n<day>22</day>\n<hour>14</hour>\n<minute>25</minute>\n<second>32</second>\n</startTime>\n<endTime>\n<year>2026</year>\n<month>10</month>\n<day>22</day>\n<hour>14</hour>\n<minute>25</minute>\n<second>50</second>\n</endTime>\n</FileInfo>\n</FileInfoList>\n</body>\n"
	if got != want {
		t.Fatalf("download request differs from the client's:\n%s", got)
	}
	if !strings.Contains(string(handleDoc(0, 2)), "<handle>2</handle>") {
		t.Fatal("handle not carried")
	}
}

func TestTheFindReplyCarriesOnlyTheHandle(t *testing.T) {
	doc, err := os.ReadFile("testdata/sd-find.xml")
	if err != nil {
		t.Fatal(err)
	}
	handle, files, err := parseFiles(doc)
	if err != nil {
		t.Fatal(err)
	}
	if handle != 1 || len(files) != 0 {
		t.Fatalf("handle %d, %d files; want the handle and no files", handle, len(files))
	}
}
