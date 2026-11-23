package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Layer is the user-layer data dict produced by user_layer_of(). The field
// order here is the on-disk JSON order; it must stay fixed so a no-change
// scan leaves the file byte-identical.
type Layer struct {
	Note          []string
	SkPaths       []string
	PvbPaths      []string
	PoseExclude   []string
	PoseExcludeVC map[string]int
	Active        *string
	ModVC         map[string]int
	PosVariants   map[string][]string
	ModDelta      map[string]*Delta
	ModDeltaOrder []string

	// Skipped holds per-set reasons for sets that were dropped from this
	// layer. It is report-only and never written into mods.json: the caller
	// surfaces it to the user so one bad set does not abort the whole run.
	// json:"-" keeps the on-disk bytes identical to before this field existed.
	Skipped []string `json:"-"`
}

// Delta is one baked vertex-space bucket, shaped like panel.Delta but kept
// local so the scan package does not depend on the render/panel packages.
type Delta struct {
	VC      int       `json:"vc"`
	Sparse  string    `json:"sparse"`
	Base    string    `json:"base"`
	BaseRef string    `json:"base_ref,omitempty"`
	Pos     []PosSpec `json:"pos,omitempty"`
}

// PosSpec is one body-shape variant of a set.
type PosSpec struct {
	Path    string `json:"path"`
	Base    string `json:"base"`
	BaseRef string `json:"base_ref,omitempty"`
}

// PosVCCrossCheck returns (vc, path) from the raw mesh on disk. vc == -1 means
// the byte size is not a multiple of 12; nil vc means the file is unreadable.
func posVC(rec *Rec, modsRoot string) (int, string) {
	if rec.RelDisk == "" || rec.PosbufFile == "" {
		return 0, ""
	}
	base := filepath.Dir(filepath.Join(append([]string{modsRoot}, strings.Split(rec.RelDisk, "/")...)...))
	p := filepath.Join(append([]string{base}, strings.Split(strings.ReplaceAll(rec.PosbufFile, "\\", "/"), "/")...)...)
	st, err := os.Stat(p)
	if err != nil {
		return 0, p
	}
	if st.Size()%12 != 0 {
		return -1, p
	}
	return int(st.Size() / 12), p
}

// appendUnique appends p only if seen does not already contain it. Two sets
// declaring the same namespace resolve to the same resource path; writing it
// twice makes FragHit in the render layer see an ambiguous match (2 of N
// pvb_paths) and drop the raw set that needed a delta.
func appendUnique(list []string, seen map[string]bool, p string) []string {
	if seen[p] {
		return list
	}
	seen[p] = true
	return append(list, p)
}

// UserLayer derives the user-layer data from a claim result.
//
// A set whose data is inconsistent is dropped (with a reason recorded in
// Layer.Skipped) instead of aborting the whole run: one bad mod must not cost
// the user every other good set. errs is reserved for failures that make it
// unsafe to write anything at all for this character -- currently only the
// case where a baked delta is required but no usable set remains.
func UserLayer(res *ClaimResult, modsRoot string, needActive bool) (*Layer, []string) {
	// First pass: decide which sets are usable. Every reason here is per-set,
	// so it skips only that set; the rest of the character still configures.
	rels := make([]string, 0, len(res.FoundKeys))
	var skipped []string
	for _, r := range res.FoundKeys {
		rec := res.Found[r]
		if rec.Kind == KindUnknown || rec.Kind == KindRawSuspect {
			skipped = append(skipped, fmt.Sprintf("`%s` classified as '%s' (degraded)", r, rec.Kind))
			continue
		}
		vals := rec.CVCVals
		if rec.Kind == KindFinished {
			if len(vals) == 0 {
				skipped = append(skipped, fmt.Sprintf("`%s` is baked-type but has no custom_vertex_count assignment", r))
				continue
			}
			if len(vals) > 1 {
				skipped = append(skipped, fmt.Sprintf("`%s` has several different custom_vertex_count values %v", r, vals))
				continue
			}
		}
		if len(vals) == 1 {
			bvc, where := posVC(rec, modsRoot)
			if bvc == -1 {
				skipped = append(skipped, fmt.Sprintf("`%s`: raw mesh `%s` byte size is not a multiple of 12", r, where))
				continue
			}
			if bvc > 0 && bvc < vals[0] {
				skipped = append(skipped, fmt.Sprintf("`%s`: too few vertices -- ini says %d, mesh `%s` holds %d", r, vals[0], where, bvc))
				continue
			}
		}
		rels = append(rels, r)
	}

	baked := []string{}
	vc := map[string]int{}
	// pose_exclude keys must be resolvable by the render layer's pathOf. A
	// namespace-declaring mod stores its pvb at Resource\<ns>\..., which the
	// ini-relative path cannot reach; for those the resource path itself is
	// the key (pathOf accepts a direct hit). AutoBake matches exclusions by
	// the same key, so both sides stay consistent.
	peKey := func(r string) string {
		if ns := res.Found[r].Namespace; ns != "" {
			return `Resource\` + ns + `\` + ResPVB
		}
		return r
	}
	for _, r := range rels {
		if res.Found[r].Kind != KindFinished {
			continue
		}
		k := peKey(r)
		baked = append(baked, k)
		vals := res.Found[r].CVCVals
		if len(vals) == 1 {
			vc[k] = vals[0]
		}
	}

	// mod_vc covers every usable set (including disabled ones); prefix is ignored.
	modVC := map[string]int{}
	for _, r := range rels {
		vals := res.Found[r].CVCVals
		if len(vals) != 1 {
			continue
		}
		modVC[r] = vals[0]
	}

	var active *string
	if len(rels) > 0 {
		s := rels[0] // sorted; a label, not a criterion
		active = &s
	}
	var errs []string
	if needActive && active == nil {
		errs = append(errs, "no usable mod directory for this character, but the user layer still carries a baked delta")
	}

	sk := make([]string, 0, len(rels))
	pvb := make([]string, 0, len(rels))
	seenSK := map[string]bool{}
	seenPVB := map[string]bool{}
	for _, r := range rels {
		// A file that declares its own namespace has every section renamed, so
		// the path form no longer resolves there; the namespace form is the one
		// 3DMigoto actually stores. Every other file keeps the path form, which
		// is what the game derives from the ini's location.
		if ns := res.Found[r].Namespace; ns != "" {
			sk = appendUnique(sk, seenSK, `Resource\`+ns+`\`+ResSK)
			pvb = appendUnique(pvb, seenPVB, `Resource\`+ns+`\`+ResPVB)
			continue
		}
		rk := strings.ReplaceAll(r, "/", "\\")
		sk = appendUnique(sk, seenSK, PathHeadMods+rk+`\`+ResSK)
		pvb = appendUnique(pvb, seenPVB, PathHeadMods+rk+`\`+ResPVB)
	}

	out := &Layer{
		SkPaths:       sk,
		PvbPaths:      pvb,
		PoseExclude:   baked,
		PoseExcludeVC: vc,
		Active:        active,
		ModVC:         modVC,
		Skipped:       skipped,
	}

	// pos_variants: only for value-route characters (no native_sparse).
	if !needActive && modsRoot != "" {
		pv := map[string][]string{}
		for _, r := range rels {
			mvc, ok := modVC[r]
			if !ok || mvc < 0 {
				continue
			}
			ini := filepath.Join(append([]string{modsRoot}, strings.Split(res.Found[r].RelDisk, "/")...)...)
			if _, err := os.Stat(ini); err != nil {
				continue
			}
			got := PosVariants(ini, r, mvc, filepath.Dir(ini))
			if len(got) > 1 {
				names := make([]string, 0, len(got))
				for _, row := range got {
					names = append(names, row[0])
				}
				pv[r] = names
			}
		}
		if len(pv) > 0 {
			out.PosVariants = pv
		}
	}
	return out, errs
}

// PosVariants finds every position stream a set's vb0 can bind to. Returns
// rows of (resource full name, base file name, mesh file relative to ini dir).
func PosVariants(iniPath, iniRel string, vc int, modDir string) [][3]string {
	b, err := os.ReadFile(iniPath)
	if err != nil {
		return nil
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")

	var cand []string
	for _, m := range reVB0Assign.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(strings.SplitN(m[1], ";", 2)[0])
		if raw == "" || strings.HasPrefix(strings.ToLower(raw), "ref ") {
			continue
		}
		name := strings.Fields(raw)[0]
		if strings.EqualFold(name, "null") || containsString(cand, name) {
			continue
		}
		cand = append(cand, name)
	}

	ns := PathHeadMods + strings.ReplaceAll(iniRel, "/", "\\")
	var rows [][3]string
	for _, name := range cand {
		body := sectionOf(text, name)
		if body == "" {
			continue
		}
		mf := reFilename.FindStringSubmatch(body)
		if mf == nil {
			continue
		}
		fname := strings.TrimSpace(strings.SplitN(mf[1], ";", 2)[0])
		ms := regexpMustCompile(`(?im)^\s*stride\s*=\s*(\d+)\s*$`).FindStringSubmatch(body)
		mg := regexpMustCompile(`(?im)^\s*format\s*=\s*(\S+)\s*$`).FindStringSubmatch(body)
		if ms == nil {
			continue
		}
		stride, err := strconv.Atoi(ms[1])
		if err != nil || stride != 12 {
			continue
		}
		if mg == nil || !strings.Contains(strings.ToUpper(mg[1]), "R32G32B32_FLOAT") {
			continue
		}
		p := filepath.Join(append([]string{modDir}, strings.Split(strings.ReplaceAll(fname, "\\", "/"), "/")...)...)
		st, err := os.Stat(p)
		if err != nil || st.Size()%12 != 0 || int(st.Size()/12) < vc {
			continue
		}
		short := name
		if strings.HasPrefix(strings.ToLower(name), "resource") {
			short = name[len("Resource"):]
		}
		rows = append(rows, [3]string{short, fname})
	}
	if len(rows) == 0 {
		return nil
	}

	var def *[3]string
	for i := range rows {
		if strings.EqualFold(rows[i][0], "PositionBuffer") {
			def = &rows[i]
			break
		}
	}
	rest := [][3]string{}
	for i := range rows {
		if def != nil && &rows[i] == def {
			continue
		}
		rest = append(rest, rows[i])
	}
	sort.SliceStable(rest, func(i, j int) bool {
		return strings.ToLower(rest[i][1]) < strings.ToLower(rest[j][1])
	})

	var out [][3]string
	if def != nil {
		out = append(out, *def)
	}
	out = append(out, rest...)
	for i := range out {
		slug := ""
		if !strings.EqualFold(out[i][0], "PositionBuffer") {
			slug = posSlug(out[i][1])
		}
		out[i] = [3]string{ns + "\\" + out[i][0], baseNameOf(vc, slug), out[i][1]}
	}
	return out
}

func posSlug(bufRel string) string {
	base := strings.Split(strings.ReplaceAll(bufRel, "\\", "/"), "/")
	stem := base[len(base)-1]
	if i := strings.LastIndex(stem, "."); i >= 0 {
		stem = stem[:i]
	}
	stem = strings.ToLower(stem)
	stem = strings.TrimPrefix(stem, "position")
	var b strings.Builder
	for _, r := range stem {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 16 {
		s = s[:16]
	}
	return s
}

func baseNameOf(vc int, slug string) string {
	if slug == "" {
		return fmt.Sprintf("ep_mod_base_%d.buf", vc)
	}
	return fmt.Sprintf("ep_mod_base_%d_%s.buf", vc, slug)
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
