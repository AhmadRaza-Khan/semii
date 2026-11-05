package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDate(t *testing.T) {
	d := NewDate(2024, time.February, 29)
	b, _ := json.Marshal(struct {
		D Date  `json:"d"`
		P *Date `json:"p,omitempty"`
	}{D: d})
	if string(b) != `{"d":"2024-02-29"}` {
		t.Errorf("%s", b)
	}
	var got struct{ D, N Date }
	if err := json.Unmarshal([]byte(`{"D":"2024-02-29","N":null}`), &got); err != nil || !got.D.Equal(d.Time) || !got.N.IsZero() {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{`{"D":"2024-02-30"}`, `{"D":"2024-02-29T00:00:00Z"}`, `{"D":5}`} {
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if txt, _ := d.MarshalText(); string(txt) != "2024-02-29" || d.String() != "2024-02-29" {
		t.Errorf("%s", txt)
	}
	if err := new(Date).UnmarshalText([]byte("x")); err == nil {
		t.Error("bad text accepted")
	}
	var nilDate *Date
	if nilDate.TimePtr() != nil || DateFromPtr(nil) != nil {
		t.Error("nil handling")
	}
	tm := d.Time
	if DateFromPtr(&tm).TimePtr().Equal(d.Time) == false {
		t.Error("round trip")
	}

	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"due":"31/12/2024"}`))
	r.Header.Set("Content-Type", "application/json")
	_, err := Decode[struct {
		Due Date `json:"due"`
	}](r)
	if e, ok := err.(*Error); !ok || len(e.Fields) != 1 || e.Fields[0].Field != "due" || e.Fields[0].Message != "must be a date in YYYY-MM-DD format" {
		t.Errorf("decode error: %#v", err)
	}

	type q struct {
		Due *Date `query:"due"`
	}
	v, err2 := DecodeQuery[q](httptest.NewRequest("GET", "/?due=2024-01-02", nil))
	if err2 != nil || v.Due.String() != "2024-01-02" {
		t.Errorf("%+v %v", v, err2)
	}
}
