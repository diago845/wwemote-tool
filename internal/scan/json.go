package scan

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// json2Writer emits Python's json.dumps(obj, ensure_ascii=False, indent=2)
// dialect, which is what <char>.mods.json uses. (The bake manifests use
// indent=1 and have their own writer.)
type json2Writer struct {
	b strings.Builder
}

func (w *json2Writer) ind(n int) {
	for i := 0; i < n*2; i++ {
		w.b.WriteByte(' ')
	}
}

func (w *json2Writer) str(s string) {
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
				const hex = "0123456789abcdef"
				w.b.WriteString(`\u`)
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

func (w *json2Writer) key(name string, level int, first *bool) {
	if !*first {
		w.b.WriteString(",\n")
	}
	*first = false
	w.ind(level)
	w.str(name)
	w.b.WriteString(": ")
}

func (w *json2Writer) stringArray(v []string, level int) {
	if len(v) == 0 {
		w.b.WriteString("[]")
		return
	}
	w.b.WriteString("[\n")
	for i, s := range v {
		if i > 0 {
			w.b.WriteString(",\n")
		}
		w.ind(level + 1)
		w.str(s)
	}
	w.b.WriteString("\n")
	w.ind(level)
	w.b.WriteByte(']')
}

func (w *json2Writer) intMap(v map[string]int, level int) {
	if len(v) == 0 {
		w.b.WriteString("{}")
		return
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.b.WriteString("{\n")
	for i, k := range keys {
		if i > 0 {
			w.b.WriteString(",\n")
		}
		w.ind(level + 1)
		w.str(k)
		w.b.WriteString(": ")
		w.b.WriteString(strconv.Itoa(v[k]))
	}
	w.b.WriteString("\n")
	w.ind(level)
	w.b.WriteByte('}')
}

func (w *json2Writer) stringMapArray(v map[string][]string, level int) {
	if len(v) == 0 {
		w.b.WriteString("{}")
		return
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.b.WriteString("{\n")
	for i, k := range keys {
		if i > 0 {
			w.b.WriteString(",\n")
		}
		w.ind(level + 1)
		w.str(k)
		w.b.WriteString(": ")
		w.stringArray(v[k], level+1)
	}
	w.b.WriteString("\n")
	w.ind(level)
	w.b.WriteByte('}')
}

func (w *json2Writer) deltas(v map[string]*Delta, order []string, level int) {
	if len(v) == 0 {
		w.b.WriteString("{}")
		return
	}
	keys := order
	if len(keys) != len(v) {
		keys = make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	w.b.WriteString("{\n")
	for i, k := range keys {
		d, ok := v[k]
		if !ok {
			continue
		}
		if i > 0 {
			w.b.WriteString(",\n")
		}
		w.ind(level + 1)
		w.str(k)
		w.b.WriteString(": {\n")
		w.ind(level + 2)
		w.str("vc")
		w.b.WriteString(": ")
		w.b.WriteString(strconv.Itoa(d.VC))
		w.b.WriteString(",\n")
		w.ind(level + 2)
		w.str("sparse")
		w.b.WriteString(": ")
		w.str(d.Sparse)
		w.b.WriteString(",\n")
		w.ind(level + 2)
		w.str("base")
		w.b.WriteString(": ")
		w.str(d.Base)
		if d.BaseRef != "" {
			w.b.WriteString(",\n")
			w.ind(level + 2)
			w.str("base_ref")
			w.b.WriteString(": ")
			w.str(d.BaseRef)
		}
		if len(d.Pos) > 0 {
			w.b.WriteString(",\n")
			w.ind(level + 2)
			w.str("pos")
			w.b.WriteString(": [\n")
			for j, p := range d.Pos {
				if j > 0 {
					w.b.WriteString(",\n")
				}
				w.ind(level + 3)
				w.b.WriteString("{\n")
				w.ind(level + 4)
				w.str("path")
				w.b.WriteString(": ")
				w.str(p.Path)
				w.b.WriteString(",\n")
				w.ind(level + 4)
				w.str("base")
				w.b.WriteString(": ")
				w.str(p.Base)
				if p.BaseRef != "" {
					w.b.WriteString(",\n")
					w.ind(level + 4)
					w.str("base_ref")
					w.b.WriteString(": ")
					w.str(p.BaseRef)
				}
				w.b.WriteString("\n")
				w.ind(level + 3)
				w.b.WriteByte('}')
			}
			w.b.WriteString("\n")
			w.ind(level + 2)
			w.b.WriteByte(']')
		}
		w.b.WriteString("\n")
		w.ind(level + 1)
		w.b.WriteByte('}')
	}
	w.b.WriteString("\n")
	w.ind(level)
	w.b.WriteByte('}')
}

// DumpModsJSON serialises the layer exactly like _dump_mods_json(): _note
// first (only when present), then the data keys in a fixed order, two-space
// indent, and a trailing newline.
func DumpModsJSON(l *Layer) []byte {
	w := &json2Writer{}
	w.b.WriteString("{\n")
	first := true
	if len(l.Note) > 0 {
		w.key("_note", 1, &first)
		w.stringArray(l.Note, 1)
	}
	w.key("sk_paths", 1, &first)
	w.stringArray(l.SkPaths, 1)
	w.key("pvb_paths", 1, &first)
	w.stringArray(l.PvbPaths, 1)
	w.key("pose_exclude", 1, &first)
	w.stringArray(l.PoseExclude, 1)
	w.key("pose_exclude_vc", 1, &first)
	w.intMap(l.PoseExcludeVC, 1)
	w.key("active", 1, &first)
	if l.Active == nil {
		w.b.WriteString("null")
	} else {
		w.str(*l.Active)
	}
	w.key("mod_vc", 1, &first)
	w.intMap(l.ModVC, 1)
	if len(l.PosVariants) > 0 {
		w.key("pos_variants", 1, &first)
		w.stringMapArray(l.PosVariants, 1)
	}
	if len(l.ModDelta) > 0 {
		w.key("mod_delta", 1, &first)
		w.deltas(l.ModDelta, l.ModDeltaOrder, 1)
	}
	w.b.WriteString("\n}\n")
	return []byte(w.b.String())
}
