package bake

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsonWriter emits the exact dialect Python's json.dump(obj, indent=1,
// ensure_ascii=False) produces: one space per indent level, ": " after keys,
// and no trailing newline. Matching it lets the manifests be diffed byte for
// byte against the reference implementation's output.
type jsonWriter struct {
	b strings.Builder
}

func (w *jsonWriter) indent(n int) {
	for i := 0; i < n; i++ {
		w.b.WriteByte(' ')
	}
}

func (w *jsonWriter) str(s string) {
	w.b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			w.b.WriteString(`\"`)
		case '\\':
			w.b.WriteString(`\\`)
		case '\n':
			w.b.WriteString(`\n`)
		case '\r':
			w.b.WriteString(`\r`)
		case '\t':
			w.b.WriteString(`\t`)
		case '\b':
			w.b.WriteString(`\b`)
		case '\f':
			w.b.WriteString(`\f`)
		default:
			if r < 0x20 {
				w.b.WriteString(`\u`)
				const hex = "0123456789abcdef"
				w.b.WriteByte(hex[(r>>12)&0xf])
				w.b.WriteByte(hex[(r>>8)&0xf])
				w.b.WriteByte(hex[(r>>4)&0xf])
				w.b.WriteByte(hex[r&0xf])
			} else if r == utf8.RuneError {
				w.b.WriteString(`\ufffd`)
			} else {
				w.b.WriteRune(r)
			}
		}
	}
	w.b.WriteByte('"')
}

func (w *jsonWriter) int(v int)     { w.b.WriteString(strconv.Itoa(v)) }
func (w *jsonWriter) int64(v int64) { w.b.WriteString(strconv.FormatInt(v, 10)) }

func (w *jsonWriter) bool(v bool) {
	if v {
		w.b.WriteString("true")
	} else {
		w.b.WriteString("false")
	}
}

// pyFloat renders a float the way Python's repr does, including the "0.0"
// and "1.0" spellings for integral values.
func (w *jsonWriter) pyFloat(v float64) {
	switch {
	case math.IsNaN(v):
		w.b.WriteString("NaN")
		return
	case math.IsInf(v, 1):
		w.b.WriteString("Infinity")
		return
	case math.IsInf(v, -1):
		w.b.WriteString("-Infinity")
		return
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		// Go drops the ".0" that Python keeps.
		s += ".0"
	}
	// Go writes exponents as "1e+06"; Python uses at least two digits and a
	// sign in the same shape, so only the digit count needs padding.
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant, exp := s[:i], s[i+1:]
		sign := "+"
		if strings.HasPrefix(exp, "-") {
			sign = "-"
			exp = exp[1:]
		} else if strings.HasPrefix(exp, "+") {
			exp = exp[1:]
		}
		for len(exp) < 2 {
			exp = "0" + exp
		}
		s = mant + "e" + sign + exp
	}
	w.b.WriteString(s)
}

// JSON serialises the manifest in the reference layout.
func (m *Manifest) JSON() []byte {
	w := &jsonWriter{}
	w.b.WriteString("{\n")
	field := func(name string, first bool) {
		if !first {
			w.b.WriteString(",\n")
		}
		w.indent(1)
		w.str(name)
		w.b.WriteString(": ")
	}
	field("format", true)
	w.str(m.Format)
	field("vc", false)
	w.int(m.VC)
	field("rowstart_bytes", false)
	w.int(m.RowstartBytes)
	field("entry_stride", false)
	w.int(m.EntryStride)
	field("total_entries", false)
	w.int(m.TotalEntries)
	field("entries_bytes", false)
	w.int(m.EntriesBytes)
	field("total_bytes", false)
	w.int(m.TotalBytes)
	field("base_buf", false)
	if m.BaseBuf == "" {
		w.b.WriteString("null")
	} else {
		w.str(m.BaseBuf)
	}
	field("base_bytes", false)
	w.int64(m.BaseBytes)
	if m.BaseRef != "" {
		field("base_ref", false)
		w.str(m.BaseRef)
	}
	if len(m.ExtraRefs) > 0 {
		field("extra_refs", false)
		w.b.WriteString("{\n")
		keys := make([]string, 0, len(m.ExtraRefs))
		for k := range m.ExtraRefs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				w.b.WriteString(",\n")
			}
			w.indent(2)
			w.str(k)
			w.b.WriteString(": ")
			w.str(m.ExtraRefs[k])
		}
		w.b.WriteString("\n")
		w.indent(1)
		w.b.WriteString("}")
	}
	field("key_semantics", false)
	w.str(m.KeySemantics)
	field("mod_dir", false)
	w.str(m.ModDir)

	field("source_files", false)
	w.b.WriteString("{\n")
	for i, kv := range m.SourceFiles {
		if i > 0 {
			w.b.WriteString(",\n")
		}
		w.indent(2)
		w.str(kv[0])
		w.b.WriteString(": ")
		w.str(kv[1])
	}
	w.b.WriteString("\n")
	w.indent(1)
	w.b.WriteString("}")

	field("unit_weight", false)
	w.bool(m.UnitWeight)
	field("baked_by", false)
	w.str(m.BakedBy)
	field("bake_version", false)
	w.str(m.BakeVersion)
	field("input_sig", false)
	w.str(m.InputSig)

	field("keys", false)
	w.b.WriteString("{\n")
	for k := 0; k < KeysTotal; k++ {
		if k > 0 {
			w.b.WriteString(",\n")
		}
		w.indent(2)
		w.str(strconv.Itoa(k))
		w.b.WriteString(": {\n")
		w.indent(3)
		w.str("entries")
		w.b.WriteString(": ")
		w.int(m.Keys[k].Entries)
		w.b.WriteString(",\n")
		w.indent(3)
		w.str("nonzero_verts")
		w.b.WriteString(": ")
		w.int(m.Keys[k].NonzeroVerts)
		w.b.WriteString(",\n")
		w.indent(3)
		w.str("absmax_pos")
		w.b.WriteString(": ")
		w.pyFloat(m.Keys[k].AbsMaxPos)
		w.b.WriteString("\n")
		w.indent(2)
		w.b.WriteString("}")
	}
	w.b.WriteString("\n")
	w.indent(1)
	w.b.WriteString("}")

	w.b.WriteString("\n}")
	return []byte(w.b.String())
}
