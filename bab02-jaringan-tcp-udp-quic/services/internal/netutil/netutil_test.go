package netutil

import "testing"

func TestParseIfaceByIP(t *testing.T) {
	out := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
52: eth0@if53    inet 10.202.2.254/24 brd 10.202.2.255 scope global eth0\       valid_lft forever
54: eth1@if55    inet 10.202.3.254/24 brd 10.202.3.255 scope global eth1\       valid_lft forever`
	if got := ParseIfaceByIP(out, "10.202.3.254"); got != "eth1" {
		t.Fatalf("got %q", got)
	}
	if got := ParseIfaceByIP(out, "10.0.0.1"); got != "" {
		t.Fatalf("seharusnya kosong, dapat %q", got)
	}
}
