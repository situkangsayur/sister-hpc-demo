package netem

import (
	"reflect"
	"testing"
)

func TestCommands(t *testing.T) {
	got := Commands("eth1", Params{DelayMs: 50, JitterMs: 10, LossPct: 5, RateMbit: 2.5})
	want := [][]string{
		{"qdisc", "del", "dev", "eth1", "root"},
		{"qdisc", "add", "dev", "eth1", "root", "netem", "delay", "50ms", "10ms", "loss", "5%", "rate", "2500kbit", "limit", "10000"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if n := len(Commands("eth0", Params{})); n != 1 {
		t.Fatalf("params nol hanya menghapus, dapat %d perintah", n)
	}
}

func TestValidate(t *testing.T) {
	bad := []Params{{DelayMs: -1}, {JitterMs: 5}, {LossPct: 80}, {RateMbit: -2}}
	for _, p := range bad {
		if p.Validate() == nil {
			t.Errorf("%+v seharusnya ditolak", p)
		}
	}
	if err := (Params{DelayMs: 50, LossPct: 0.5}).Validate(); err != nil {
		t.Errorf("valid ditolak: %v", err)
	}
}

func TestString(t *testing.T) {
	if s := (Params{DelayMs: 50, LossPct: 5}).String(); s != "delay 50ms loss 5%" {
		t.Errorf("String() = %q", s)
	}
}
