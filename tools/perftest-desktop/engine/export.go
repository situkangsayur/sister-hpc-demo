package engine

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

// WriteJSON menulis Report lengkap (config, ringkasan, timeline, histogram) sebagai JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteCSV menulis timeline per interval sebagai CSV, diakhiri satu baris "total".
// Kolom errors_by_kind berisi pasangan jenis=jumlah dipisah ";".
func (r Report) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"t_s", "rps", "ok", "errors", "p50_ms", "p95_ms", "p99_ms", "mean_ms", "errors_by_kind"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	for _, p := range r.Timeline {
		_ = cw.Write([]string{f(p.T), f(p.RPS), u(p.OK), u(p.Errors), f(p.P50), f(p.P95), f(p.P99), f(p.Mean), joinKinds(p.ErrBy)})
	}
	l := r.Latency
	_ = cw.Write([]string{"total", f(r.RPS), u(r.OK), u(r.Errors), f(l.P50), f(l.P95), f(l.P99), f(l.Mean), joinKinds(r.ErrorsByKind)})
	cw.Flush()
	return cw.Error()
}

func joinKinds(m map[string]uint64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + strconv.FormatUint(m[k], 10)
	}
	return strings.Join(parts, ";")
}
