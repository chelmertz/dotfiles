package main

import (
	"reflect"
	"testing"
)

// Shape matches `i3-msg -t get_tree`: root → output → content con → workspace
// → cons; scratchpad windows sit in floating_nodes under the __i3 output.
const treeFixture = `{"id":1,"type":"root","nodes":[
 {"id":2,"type":"output","name":"eDP-1","nodes":[
  {"id":3,"type":"con","name":"content","nodes":[
   {"id":4,"type":"workspace","name":"1","nodes":[
    {"id":5,"type":"con","window":100,"focused":false,"window_properties":{"class":"com.mitchellh.ghostty","instance":"p:m/dependabot","title":"a"},"nodes":[],"floating_nodes":[]},
    {"id":6,"type":"con","window":101,"focused":true,"window_properties":{"class":"com.mitchellh.ghostty","instance":"p:m/dependabot","title":"b"},"nodes":[],"floating_nodes":[]},
    {"id":7,"type":"con","window":102,"focused":false,"window_properties":{"class":"firefox","instance":"Navigator","title":"c"},"nodes":[],"floating_nodes":[]}
   ],"floating_nodes":[]}
  ],"floating_nodes":[]}
 ],"floating_nodes":[]},
 {"id":8,"type":"output","name":"__i3","nodes":[
  {"id":9,"type":"con","name":"content","nodes":[
   {"id":10,"type":"workspace","name":"__i3_scratch","nodes":[],"floating_nodes":[
    {"id":11,"type":"floating_con","nodes":[
     {"id":12,"type":"con","window":103,"focused":false,"window_properties":{"class":"com.mitchellh.ghostty","instance":"p:personal/health","title":"d"},"nodes":[],"floating_nodes":[]}
    ],"floating_nodes":[]}
   ]}
  ],"floating_nodes":[]}
 ],"floating_nodes":[]}
],"floating_nodes":[]}`

func TestFindTagged(t *testing.T) {
	got, err := FindTagged([]byte(treeFixture), "p:m/dependabot")
	if err != nil {
		t.Fatal(err)
	}
	want := []Win{{ConID: 5, Focused: false}, {ConID: 6, Focused: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	scratch, _ := FindTagged([]byte(treeFixture), "p:personal/health")
	if len(scratch) != 1 || scratch[0].ConID != 12 {
		t.Fatalf("scratchpad window not found: %+v", scratch)
	}
	none, _ := FindTagged([]byte(treeFixture), "p:m/nope")
	if len(none) != 0 {
		t.Fatalf("got %+v", none)
	}
}

func TestOpenTags(t *testing.T) {
	got, err := OpenTags([]byte(treeFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"p:m/dependabot": true, "p:personal/health": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestPickFocus(t *testing.T) {
	cases := []struct {
		name string
		wins []Win
		want int64
		ok   bool
	}{
		{"none", nil, 0, false},
		{"one unfocused", []Win{{5, false}}, 5, true},
		{"one focused stays", []Win{{5, true}}, 5, true},
		{"none focused → first", []Win{{5, false}, {6, false}}, 5, true},
		{"cycle to next", []Win{{5, false}, {6, true}, {7, false}}, 7, true},
		{"cycle wraps", []Win{{5, false}, {6, false}, {7, true}}, 5, true},
	}
	for _, c := range cases {
		got, ok := PickFocus(c.wins)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%d,%v) want (%d,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestFindTaggedBadJSON(t *testing.T) {
	if _, err := FindTagged([]byte("{nope"), "x"); err == nil {
		t.Fatal("want error")
	}
}
