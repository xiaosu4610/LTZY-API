package middleware

import (
	"reflect"
	"testing"
)

func TestParseCorsAllowOrigins(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"https://ltzy.top", []string{"https://ltzy.top"}},
		{"https://ltzy.top , https://admin.ltzy.top/", []string{"https://ltzy.top", "https://admin.ltzy.top"}},
		{"a,  ,b,", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := parseCorsAllowOrigins(c.raw)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseCorsAllowOrigins(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}
