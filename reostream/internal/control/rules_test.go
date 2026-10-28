package control

import (
	"strings"
	"testing"
)

// Off a real pano (Duo), 2026-10-26: one line-crossing rule.
const crosslineRead = `<?xml version="1.0" encoding="UTF-8" ?>
<body> <CrosslineDetect version="1.1"> <channelId>0</channelId> <maxNum>3</maxNum> <op>none</op> <pointMinNum>2</pointMinNum> <pointMaxNUm>8</pointMaxNUm> <crosslineDetectItem> <location>0</location> <index>1</index> <name>Fence 1</name> <aiType>people,vehicle,dog_cat</aiType> <sesensitivity>50</sesensitivity> <pointList> <point> <x>0.25</x> <y>0.5</y> </point> <point> <x>0.75</x> <y>0.5</y> </point> </pointList> <direction>both</direction> </crosslineDetectItem> </CrosslineDetect> </body>`

func TestParseRulesReadsTheCamerasList(t *testing.T) {
	k := ruleKinds()[0]
	set := parseRules([]byte(crosslineRead), k)
	if set.Max != 3 || set.MinPoints != 2 || set.MaxPoints != 8 || len(set.Rules) != 1 {
		t.Fatalf("parseRules = %+v", set)
	}
	r := set.Rules[0]
	if r.Index != 1 || r.Name != "Fence 1" || r.AIType != "people,vehicle,dog_cat" || r.Sensitivity != 50 || r.Direction != "both" || r.Points != "0.25,0.5;0.75,0.5" {
		t.Errorf("rule = %+v", r)
	}
}

// The write is the Reolink app's shape; modify was proved on the pano by
// renaming a rule in place and reading it back (2026-10-26).
func TestRuleDocumentHasTheAppsShape(t *testing.T) {
	k := ruleKinds()[1] // intrusion
	doc := string(ruleDoc(k, "modify", 0, rule{Index: 2, Name: "Drive", AIType: "people", Sensitivity: 60, Stay: 3, Points: "0.1,0.1;0.9,0.1;0.9,0.9;0.1,0.9"}))
	for _, want := range []string{"<IntrusionDetect version=\"1.1\">", "<op>modify</op>", "<intrusionDetectItem>", "<index>2</index>", "<name>Drive</name>", "<sesensitivity>60</sesensitivity>", "<stayTime>3</stayTime>", "<x>1.000000e-01</x>", "<y>9.000000e-01</y>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "<direction>") {
		t.Error("an intrusion rule got a direction")
	}
	if strings.Contains(doc, "Drive<") && strings.Contains(doc, "<Drive") {
		t.Error("the name was not escaped as text")
	}
}

func TestRuleNamesAreEscaped(t *testing.T) {
	doc := string(ruleDoc(ruleKinds()[0], "add", 0, rule{Index: 1, Name: "A<b>&c", AIType: "people", Sensitivity: 50, Direction: "both", Points: "0,0;1,1"}))
	if !strings.Contains(doc, "<name>A&lt;b&gt;&amp;c</name>") {
		t.Errorf("name not escaped:\n%s", doc)
	}
}
