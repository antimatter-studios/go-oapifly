package oapifly

import (
	"encoding/json"
	"reflect"
	"testing"
)

type listsInner struct {
	Tags []string `json:"tags"`
}

type listsNode struct {
	Name string     `json:"name"`
	Next *listsNode `json:"next,omitempty"`
	Kids []string   `json:"kids"`
}

type listsResponse struct {
	Steps    []string              `json:"steps"`
	Counts   map[string]int        `json:"counts"`
	Optional []string              `json:"optional,omitempty"`
	Hidden   []string              `json:"-"`
	Blob     []byte                `json:"blob"`
	Inner    listsInner            `json:"inner"`
	Items    []listsInner          `json:"items"`
	ByName   map[string]listsInner `json:"by_name"`
	Owner    *listsInner           `json:"owner"`
	Anything interface{}           `json:"anything"`
	Page     ParityPage[string]    `json:"page"`
	private  []string
}

func encoded(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A list a handler left nil is encoded as [] and a map as {}, wherever it sits, so the
// response says what its description promises (#28).
func TestEmptyListsEncodeAsEmpty(t *testing.T) {
	in := listsResponse{
		Items:    []listsInner{{}},
		ByName:   map[string]listsInner{"a": {}},
		Owner:    &listsInner{},
		Anything: listsInner{},
	}
	got := encoded(t, EmptyLists(in))
	want := `{"steps":[],"counts":{},"blob":null,"inner":{"tags":[]},"items":[{"tags":[]}],` +
		`"by_name":{"a":{"tags":[]}},"owner":{"tags":[]},"anything":{"tags":[]},"page":{"data":[],"total":0}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// encoding/json's own rules are kept: an omitempty field is omitted, json:"-" is untouched,
// a []byte stays a base64 string, unexported fields are not touched, and a nil pointer
// stays null, since a pointer is nullable on purpose.
func TestEmptyListsFollowsEncodingJSON(t *testing.T) {
	out := EmptyLists(listsResponse{})
	if out.Optional != nil || out.Hidden != nil || out.Blob != nil || out.private != nil || out.Owner != nil {
		t.Fatalf("left alone: %+v", out)
	}
	if out.Steps == nil || out.Counts == nil {
		t.Fatalf("filled: %+v", out)
	}
}

// The value passed in is not changed.
func TestEmptyListsDoesNotModifyItsArgument(t *testing.T) {
	in := &listsResponse{Owner: &listsInner{}}
	out := EmptyLists(in)
	if in.Steps != nil || in.Owner.Tags != nil {
		t.Fatalf("the argument was modified: %+v", in)
	}
	if out.Steps == nil || out.Owner.Tags == nil || out.Owner == in.Owner {
		t.Fatalf("the copy is filled: %+v", out)
	}
}

// A value that reaches itself through a pointer is walked without looping.
func TestEmptyListsWithACycle(t *testing.T) {
	a := &listsNode{Name: "a"}
	a.Next = a
	out := EmptyLists(a)
	if out.Kids == nil {
		t.Fatalf("the first node is filled: %+v", out)
	}
}

// Lists at the top level and nil pointers are handled too.
func TestEmptyListsTopLevel(t *testing.T) {
	if got := encoded(t, EmptyLists([]listsInner(nil))); got != `[]` {
		t.Fatalf("a nil top-level list: %s", got)
	}
	if got := encoded(t, EmptyLists((*listsInner)(nil))); got != `null` {
		t.Fatalf("a nil pointer stays null: %s", got)
	}
}

type listsAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type listsMessage struct {
	Subject     string            `json:"subject"`
	Attachments []listsAttachment `json:"attachments"`
}

// A part whose type cannot hold a list is returned as it is: a large []byte is shared, not
// copied byte by byte, while the lists around it are still filled.
func TestEmptyListsSharesWhatCannotHoldAList(t *testing.T) {
	data := make([]byte, 1<<20)
	in := listsMessage{Attachments: []listsAttachment{{Name: "a", Data: data}}}
	out := EmptyLists(in)
	if &out.Attachments[0].Data[0] != &data[0] {
		t.Fatal("the attachment's bytes were copied")
	}
	if got := EmptyLists(listsMessage{}); got.Attachments == nil {
		t.Fatalf("the list is filled: %+v", got)
	}
}

// Which types can hold a list, including types that refer to themselves.
func TestMayHoldList(t *testing.T) {
	type leaf struct {
		Name  string
		Next  *leaf
		Bytes []byte
		skip  []string
		Gone  []string `json:"-"`
	}
	type branch struct {
		Self *branch
		Kids []string
	}
	type viaPointer struct{ B *branch }
	cases := []struct {
		v    interface{}
		want bool
	}{
		{"", false},
		{0, false},
		{[]byte(nil), false},
		{[4]byte{}, false},
		{leaf{}, false},
		{&leaf{}, false},
		{[]string(nil), true},
		{map[string]int(nil), true},
		{[2][]int{}, true},
		{branch{}, true},
		{viaPointer{}, true},
		{listsResponse{}, true},
	}
	for _, c := range cases {
		if got := mayHoldList(reflect.TypeOf(c.v)); got != c.want {
			t.Errorf("%T: %v, want %v", c.v, got, c.want)
		}
	}
	if got := mayHoldList(reflect.TypeOf((*interface{})(nil)).Elem()); !got {
		t.Error("an interface can hold anything")
	}
}
