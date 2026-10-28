package control

import (
	"strings"
	"testing"
)

// A block name may carry a variant after "#": the same read/write pair,
// read with a request document naming what to read. The per-type AI
// settings are the case (342 with <type>people</type>, captured from the
// Reolink app 2026-10-26).
func TestBlockVariantResolvesToItsPairAndRequest(t *testing.T) {
	p, ok := pairForName("ai detect cfg get#people")
	if !ok || p.Get != 342 || p.Set != 343 {
		t.Fatalf("pairForName(ai detect cfg get#people) = %+v, %v; want 342/343", p, ok)
	}
	req := blockRequest("ai detect cfg get#vehicle", 0)
	for _, want := range []string{"<AiDetectCfg", "<chn>0</chn>", "<type>vehicle</type>"} {
		if !strings.Contains(string(req), want) {
			t.Errorf("request %q lacks %q", req, want)
		}
	}
	if blockRequest("isp get", 0) != nil {
		t.Error("a plain block got a request document")
	}
}

// The three AI groups edit the same paths in three documents; their values
// must not overwrite each other on the page.
func TestVariantFieldsHaveDistinctKeys(t *testing.T) {
	seen := map[string]string{}
	for _, g := range groups() {
		for _, f := range g.Fields {
			if f.XPath == "" {
				continue
			}
			k := f.Key(f.XPath)
			if prev, dup := seen[k]; dup && prev != g.Title {
				t.Errorf("value key %q used by both %q and %q", k, prev, g.Title)
			}
			seen[k] = g.Title
		}
	}
	if _, ok := seen["people:AiDetectCfg/sensitivity"]; !ok {
		t.Error("person detection sensitivity is not declared")
	}
}

// Record audio is one switch but two elements: the main and sub stream
// each carry their own <audio>. Both change together.
func TestAlsoPathsAreWrittenWithTheSameValue(t *testing.T) {
	doc := []byte(`<body><Compression><mainStream><audio>0</audio></mainStream><subStream><audio>0</audio></subStream><thirdStream><audio>0</audio></thirdStream></Compression></body>`)
	edits, err := expandAlso("get enc", []edit{{XPath: "mainStream/audio", Value: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edits {
		if doc, err = setField(doc, e.XPath, e.Value); err != nil {
			t.Fatal(err)
		}
	}
	s := string(doc)
	if !strings.Contains(s, "<mainStream><audio>1</audio>") || !strings.Contains(s, "<subStream><audio>1</audio>") {
		t.Errorf("doc after record-audio on: %s", s)
	}
	if !strings.Contains(s, "<thirdStream><audio>0</audio>") {
		t.Errorf("the third stream was touched: %s", s)
	}
}

// Motion sensitivity is stored upside down: the Reolink app shows 51 minus
// the camera's value (its slider at 45 sent 6, at 41 sent 10; captured
// 2026-10-26). reostream shows and takes the app's number.
func TestInvertedFieldsShowAndWriteTheAppsNumber(t *testing.T) {
	var f Field
	for _, g := range groups() {
		for _, x := range g.Fields {
			if x.Slug == "md-sensitivity" {
				f = x
			}
		}
	}
	if f.Invert != 51 {
		t.Fatalf("md-sensitivity Invert = %d, want 51", f.Invert)
	}
	if got := f.shown("10"); got != "41" {
		t.Errorf("shown(10) = %q, want 41", got)
	}
	if got, err := f.stored("45"); err != nil || got != "6" {
		t.Errorf("stored(45) = %q, %v; want 6", got, err)
	}
	if _, err := f.stored("fifty"); err == nil {
		t.Error("a non-number was accepted for an inverted field")
	}
}
