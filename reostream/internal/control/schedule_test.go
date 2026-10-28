package control

import (
	"strings"
	"testing"
)

const schedDoc = `<?xml version="1.0" encoding="UTF-8" ?>
<body>
<Record version="1.1">
<channelId>0</channelId>
<enable>1</enable>
<typeScheduleList>
<item>
<type>MD</type>
<valueTable>` + "111" + `</valueTable>
</item>
<item>
<type>people</type>
<valueTable>101</valueTable>
</item>
<item>
<type>crossline</type>
<index>0</index>
<valueTable>000</valueTable>
</item>
<item>
<type>crossline</type>
<index>1</index>
<valueTable>010</valueTable>
</item>
</typeScheduleList>
</Record>
</body>
`

func TestParseSchedulesListsEveryItem(t *testing.T) {
	items := parseSchedules([]byte(schedDoc))
	want := []schedItem{{"MD", -1, "111"}, {"people", -1, "101"}, {"crossline", 0, "000"}, {"crossline", 1, "010"}}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
}

// Only the named item's table changes; every other byte stays as the
// camera sent it.
func TestSetScheduleReplacesOneItemsTable(t *testing.T) {
	out, err := setSchedule([]byte(schedDoc), "crossline", 1, "111")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(schedDoc, "<index>1</index>\n<valueTable>010</valueTable>", "<index>1</index>\n<valueTable>111</valueTable>", 1)
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	if _, err := setSchedule([]byte(schedDoc), "dog_cat", -1, "111"); err == nil {
		t.Error("an item the camera does not have was invented")
	}
	if _, err := setSchedule([]byte(schedDoc), "MD", -1, "11"); err == nil {
		t.Error("a table of the wrong length was accepted")
	}
	if _, err := setSchedule([]byte(schedDoc), "MD", -1, "1x1"); err == nil {
		t.Error("a table with something other than 0 and 1 was accepted")
	}
}

// The tracking schedule is one table, not a list of items.
func TestTrackScheduleIsASingleTable(t *testing.T) {
	doc := []byte("<body>\n<trackSchedule version=\"1.1\">\n<channelId>0</channelId>\n<timeTable>1010</timeTable>\n</trackSchedule>\n</body>\n")
	items := parseSchedules(doc)
	if len(items) != 1 || items[0] != (schedItem{"track", -1, "1010"}) {
		t.Fatalf("items = %+v", items)
	}
	out, err := setSchedule(doc, "track", -1, "0000")
	if err != nil || !strings.Contains(string(out), "<timeTable>0000</timeTable>") {
		t.Fatalf("setSchedule = %s, %v", out, err)
	}
}

// Off a real TrackMix: one 168-slot table (7 days of 24 hours) per type.
func TestParseSchedulesOnARealRecordSchedule(t *testing.T) {
	items := parseSchedules(readTestdata(t, "testdata/livefixtures/trackmix-rectask.xml"))
	got := map[string]int{}
	for _, it := range items {
		got[it.Type] = len(it.Table)
	}
	for _, typ := range []string{"MD", "Normal", "people", "vehicle", "dog_cat"} {
		if got[typ] != 168 {
			t.Errorf("%s table has %d slots, want 168 (items %v)", typ, got[typ], got)
		}
	}
}
