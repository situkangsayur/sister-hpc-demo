package presets

import (
	"strings"
	"testing"
)

// Semua preset harus valid, memakai ${BIND_ADDR}, dan port-nya di rentang konvensi.
func TestBuiltinValid(t *testing.T) {
	ps := Builtin()
	if len(ps) < 10 {
		t.Fatalf("preset terlalu sedikit: %d", len(ps))
	}
	kinds := map[string]bool{}
	for _, p := range ps {
		c := p.Config.Expand(map[string]string{"BIND_ADDR": "127.0.0.1"})
		if _, err := c.Validate(); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
		addr := p.Config.URL + p.Config.Target
		if !strings.Contains(addr, "${BIND_ADDR}") {
			t.Errorf("%s tidak memakai ${BIND_ADDR}: %s", p.Name, addr)
		}
		if p.Bab > 0 {
			want := ":50" + string(rune('0'+p.Bab))
			if !strings.Contains(addr, want) {
				t.Errorf("%s: port di luar rentang bab %d: %s", p.Name, p.Bab, addr)
			}
		}
		kinds[string(p.Config.Kind)] = true
	}
	for _, k := range []string{"http", "graphql", "grpc", "ws"} {
		if !kinds[k] {
			t.Errorf("tidak ada preset jenis %s", k)
		}
	}
}
