package render

import (
	"regexp"
	"strconv"
	"strings"
)

// Output file identities. A section belongs to exactly one of them, and that
// decision is what both the generator and the splitter consult -- they must
// never disagree, or a section lands in a file whose relative paths do not
// resolve and 3DMigoto drops it silently.
const (
	OUT_CORE = iota + 1
	OUT_MODS
	OUT_CHAR
)

// owner is a resolved destination: one of the kinds above, plus the character
// name for OUT_CHAR.
type owner struct {
	Kind int
	Cap  string
}

var (
	rePosed     = regexp.MustCompile(`^ResourceEpPosed(?:Src|RW)(\d+)(?:_\d+)?$`)
	reTok       = regexp.MustCompile(`^ResourceEpTok(\d+)$`)
	rePoseApply = regexp.MustCompile(`^CommandListEpPoseApply(\d+)$`)
	reNativeVal = regexp.MustCompile(`^(?:` +
		`ResourceEpNativeCB(?:RW|Staging)` +
		`|CustomShaderEpNative(?:Cb|Load)` +
		`|CommandListEpNativeValue` +
		`|CustomShaderEpU5Shield` +
		`|CommandListEpU5Shield` +
		`|ResourceEpNative(?:Src|RW|In|Out|Offset|Sparse)` +
		`|CommandListEpNativeBake` +
		`|CustomShaderEpNativePose` +
		`|ResourceEpNativeDelta` +
		`|CustomShaderEpNativeDelta` +
		`)(\d+)(?:K\d+)?$`)

	reModsBind  = regexp.MustCompile(`^CommandListEpBindU5([A-Za-z]+)$`)
	reModsCid   = regexp.MustCompile(`^(?:CommandListEpU5Shield|CustomShaderEpU5Shield` + `|ResourceEpPosed(?:Src|RW)|CommandListEpPoseApply)(\d+)(?:_\d+)?$`)
	reModsDelta = regexp.MustCompile(`^(?:ResourceEpModDelta\d+K\d+|ResourceEpModRW\d+(?:_\d+)*` +
		`|ResourceEpModBase\d+(?:_\d+)*|ResourceEpModSparse\d+(?:_\d+)*` +
		`|CustomShaderEpModDelta\d+|CustomShaderEpModSparse\d+(?:_\d+)*)$`)
	reModsCap = regexp.MustCompile(`^(?:TextureOverrideEpPose|CommandListEpPose` +
		`|TextureOverrideEpModDelta|CommandListEpModDelta)([A-Za-z]+)$`)
	reModsCidFam = regexp.MustCompile(`^(?:ResourceEpModRW|ResourceEpModSparse|ResourceEpModBase` +
		`|CustomShaderEpModSparse)(\d+)(?:_\d+)*$`)

	reChrVar       = regexp.MustCompile(`^\$ep_chr_([a-z]+)$`)
	reNativeOffVar = regexp.MustCompile(`^\$ep_native_off_([a-z]+)$`)
	reChannelVar   = regexp.MustCompile(`^\$ep_(?:did|cid|cw)(\d+)$`)
	reKeepVar      = regexp.MustCompile(`^\$ep_keep(\d+)_`)
	reNatPosedVar  = regexp.MustCompile(`^\$ep_nat_posed(\d+)$`)
	reModsVar      = regexp.MustCompile(`^\$ep_r_(?:base|bake|off|skip)\d+$`)
	reGlobalDecl   = regexp.MustCompile(`^\s*global\s+(?:persist\s+)?(\$[A-Za-z_][\w]*)`)
	reSectionHead  = regexp.MustCompile(`^\[([^\]]+)\]\s*$`)
	reNamespace    = regexp.MustCompile(`^\s*namespace\s*=`)
	reBlockPlace   = regexp.MustCompile(`^([ \t]*);\s*@@BLOCK:([A-Z_]+)@@\s*$`)
)

// This group must stay in the main file even though its body branches per
// character: each name is a singleton, so splitting it per character would
// collide (the later definition silently wins after include flattening).
var stayCore = map[string]bool{
	"CommandListEpCharReset": true,
	"CommandListEpCharFrame": true,
	"KeyEpRouteR":            true,
}

// commonVars are the per-frame quantities every character's sections share.
// They are read by the main file's [Present] and [CommandListEpCharFrame], so a
// build that installs only one character must still find them declared.
func commonVars() map[string]bool {
	return map[string]bool{
		"$ep_chr_any": true, "$ep_chr_id": true, "$ep_chr_prev": true,
		"$ep_rcp_src":    true,
		"$ep_switch_req": true, "$ep_switch_prev": true, "$ep_switch_edge": true,
		"$ep_from": true, "$ep_next_ok": true,
		"$ep_nat_toggle_req": true, "$ep_nat_toggle_prev": true, "$ep_nat_edge": true,
		"$ep_chr_n":    true,
		"$ep_nat_cur":  true,
		"$ep_av_hover": true, "$ep_av_click": true, "$ep_av_flash": true,
	}
}

func (g *Gen) caps() []string {
	out := make([]string, 0, len(g.T.Chars))
	for _, c := range g.T.Chars {
		out = append(out, c.Cap())
	}
	return out
}

// capOfCid maps a cid to the capitalised character name.
func (g *Gen) capOfCid(cid int) string {
	for _, c := range g.T.Chars {
		if c.Cid == cid {
			return c.Cap()
		}
	}
	return ""
}

// capSuffixOf reports the registered character whose name ends the section
// name, preferring the longest match so that a name which is a prefix of
// another one cannot steal it.
func (g *Gen) capSuffixOf(name string) string {
	hit := ""
	for _, cap := range g.caps() {
		if strings.HasSuffix(name, cap) && len(cap) > len(hit) {
			hit = cap
		}
	}
	return hit
}

// tokOwner maps an emote resource id to the character that owns it.
func (g *Gen) tokOwner(tidStr string) string {
	tid, err := strconv.Atoi(tidStr)
	if err != nil {
		return ""
	}
	for _, c := range g.T.Chars {
		for _, t := range c.Tokens {
			if t.Tid == tid {
				if t.Char == "" {
					return ""
				}
				return strings.ToUpper(t.Char[:1]) + t.Char[1:]
			}
		}
	}
	return ""
}

// sectionOwner maps a section name to the character it belongs to; "" means it
// stays in the main file.
func (g *Gen) sectionOwner(name string) string {
	if stayCore[name] {
		return ""
	}
	if cap := g.capSuffixOf(name); cap != "" {
		return cap
	}
	for _, re := range []*regexp.Regexp{rePosed, rePoseApply, reNativeVal, reModsCidFam} {
		if m := re.FindStringSubmatch(name); m != nil {
			if cid, err := strconv.Atoi(m[1]); err == nil {
				return g.capOfCid(cid)
			}
		}
	}
	if m := reTok.FindStringSubmatch(name); m != nil {
		return g.tokOwner(m[1])
	}
	return ""
}

// modsOwner reports whether a section is driven purely by user-layer data.
//
// The rule is "whose data drives it", not "which chain it hangs off": paths and
// vertex counts come from the scanner (user), while anchor hashes, layouts and
// recipes are authored (frozen in the binary).
func (g *Gen) modsOwner(name string) bool {
	if reModsBind.MatchString(name) || reModsCid.MatchString(name) || reModsDelta.MatchString(name) {
		return true
	}
	// The Cap form must be checked against the closed set of registered
	// characters: `CommandListEpPoseApply0` also matches `CommandListEpPose`
	// plus `Apply0`, and would be assigned to a character that does not exist.
	if m := reModsCap.FindStringSubmatch(name); m != nil {
		for _, cap := range g.caps() {
			if m[1] == cap {
				return true
			}
		}
	}
	return false
}

// outputFileOf is the single mapping from section name to destination, shared
// by the generator and the splitter.
//
// Order is load-bearing: the user layer is tested first, because
// `TextureOverrideEpPoseSuisui` matches both the character-suffix rule and the
// user-layer rule.
func (g *Gen) outputFileOf(name string) owner {
	if g.modsOwner(name) {
		return owner{Kind: OUT_MODS}
	}
	if cap := g.sectionOwner(name); cap != "" {
		return owner{Kind: OUT_CHAR, Cap: cap}
	}
	return owner{Kind: OUT_CORE}
}

// csPrefix is the directory prefix a relative shader path needs, given where
// the section actually lands. Depth 0 yields ".\" rather than "" -- the two are
// equivalent to the host but differ in bytes, and byte equality is the check
// that this rewrite is faithful.
func csPrefix(o owner) string {
	switch o.Kind {
	case OUT_CHAR:
		return strings.Repeat("..\\", charINIDepth)
	case OUT_MODS:
		return strings.Repeat("..\\", strings.Count(modsINISubdir, "/")+1)
	}
	return ".\\"
}

func (g *Gen) csRef(logicalRel, sectionName string) string {
	return csPrefix(g.outputFileOf(sectionName)) + logicalRel
}

// roleVarOf maps a per-character variable name to its character.
func (g *Gen) roleVarOf(v string) string {
	if m := reChrVar.FindStringSubmatch(v); m != nil {
		for _, c := range g.T.Chars {
			if c.Name == m[1] {
				return c.Cap()
			}
		}
		return ""
	}
	if m := reNativeOffVar.FindStringSubmatch(v); m != nil {
		for _, c := range g.T.Chars {
			if c.Name == m[1] {
				return c.Cap()
			}
		}
		return ""
	}
	// cid and slot index are concatenated without a separator, so `(\d+)\d+`
	// would borrow a digit from the slot once k >= 10. Enumerate the known cids
	// instead and take the longest prefix.
	if m := reChannelVar.FindStringSubmatch(v); m != nil {
		digits := m[1]
		best := ""
		for _, c := range g.T.Chars {
			p := strconv.Itoa(c.Cid)
			if strings.HasPrefix(digits, p) && len(digits) > len(p) && len(p) > len(best) {
				best = p
			}
		}
		if best == "" {
			return ""
		}
		cid, _ := strconv.Atoi(best)
		return g.capOfCid(cid)
	}
	for _, re := range []*regexp.Regexp{reKeepVar, reNatPosedVar} {
		if m := re.FindStringSubmatch(v); m != nil {
			cid, _ := strconv.Atoi(m[1])
			return g.capOfCid(cid)
		}
	}
	return ""
}

// modsVar reports whether a variable belongs to the user layer.
//
// Only the route-R per-set state qualifies. `$ep_native_off_*` and
// `$ep_hasmod_*` must NOT move here: they are written from character sections,
// and a declaration in a missing user-layer file turns every `if` that touches
// them into nothing at all.
func modsVar(v string) bool { return reModsVar.MatchString(v) }
