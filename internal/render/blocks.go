package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"wwemote/internal/panel"
)

// Frozen constants of the generated ini. They are named after the reference
// generator's constants so that a reader can match the two side by side.
const (
	modsININame       = "WWEmoteMods.ini"
	modsINISubdir     = "user"
	userDeltaDirname  = "user"
	userDeltaININame  = "user.ini"
	userDeltaSection  = "EpUserSparse"
	userDeltaSectLoc  = "ResourceEpModSparse"
	signatureKey      = "EP-PANEL-SIGNATURE"
	signatureRoot     = "WWEmote"
	versionKey        = "EP-PANEL-VERSION"
	skLoaderFilter    = "3381.3333"
	skMultFilter      = "3381.4444"
	nativeU5ShieldHSL = `Shaders\ep_u5_shield.hlsl`
	valueRoutePrio    = -1
	routeRSkipDbg     = "$ep_r_skip"
	epRecipeBase      = 96
	epShieldSlot      = epRecipeBase + epRecipeSlots
	epRecipeSlots     = 64
	epU5ShieldFloats  = 64
	dummyU5           = "ResourceEpDummyU5"
	charINIDepth      = 2
)

// Gen renders every generated ini from one immutable character table.
type Gen struct {
	T *panel.Table
	K int
	// UserRoot is the directory the user-layer data files live in. Every
	// `filename` the user layer writes is resolved relative to the file that
	// declares it, which is <UserRoot>/WWEmoteMods.ini.
	UserRoot string
	// ModsRoot is the real Mods/ directory. Together with UserRoot it decides
	// how many ../ a base_ref must walk; a versioned panel wrapper is one level
	// deeper than the legacy layout and used to resolve every base inside it.
	ModsRoot string
	// Version is stamped into the main file's header.
	Version string
	// shims collects namespace shims for resources whose path contains a space
	// or similar character. It is reset at the start of every render.
	shims *shimSet
	// Skipped holds per-set reasons for sets dropped while rendering (an
	// unmatched pose_exclude, or a bucket whose base/delta cannot be used).
	// It is report-only: the caller pushes each reason so one bad set does not
	// abort the whole config. Reset at the start of every render.
	Skipped []string
	// prepared guards the one-time delta filter below. targets is the filtered
	// bucket list per character; dropped chars have no usable delta at all and
	// are left out of this render rather than emitted with an empty bake route.
	prepared bool
	targets  map[*panel.Char][]panel.Target
	dropped  map[*panel.Char]bool
}

// New builds a generator; K comes from the frozen table so that adding an emote
// slot cannot desynchronise the recipe channel from the shader that reads it.
//
// Characters with no scanner data at all are dropped here. A fresh install or
// a wiped user/ dir leaves every author-layer char pose=true but pathless;
// rendering one would emit a pose override keyed by its frozen AnchorHash with
// no write-back target, silently killing the character's native expressions.
// Partially configured chars are kept so the consistency guard still catches
// real anomalies (data present but pvb_paths missing).
func New(t *panel.Table, userRoot, version string, modsRoot ...string) *Gen {
	configured := t.ConfiguredOnly()
	g := &Gen{T: configured, K: t.K, UserRoot: userRoot, Version: version, shims: newShimSet()}
	if len(modsRoot) > 0 {
		g.ModsRoot = modsRoot[0]
	}
	return g
}

// prepare filters each character's delta buckets once per render. A bucket
// whose base mesh, sparse delta or path map is unusable is dropped on its own;
// a character with no usable bucket is dropped entirely, because emitting its
// bake route from pvb_paths would write the wrong vertex space back.
func (g *Gen) prepare() error {
	if g.prepared {
		return nil
	}
	g.prepared = true
	g.targets = map[*panel.Char][]panel.Target{}
	g.dropped = map[*panel.Char]bool{}
	for _, c := range g.T.Chars {
		if len(c.ModDelta) == 0 {
			continue
		}
		targets, skips, err := c.DeltaTargetsLenient(g.UserRoot, g.ModsRoot)
		if err != nil {
			return err
		}
		for _, sk := range skips {
			g.Skipped = append(g.Skipped, fmt.Sprintf(
				"%s: set %q skipped -- %s (mod file: %s)",
				c.Label, sk.Frag, sk.Reason, sk.ModFile))
		}
		if len(targets) == 0 {
			g.dropped[c] = true
			g.Skipped = append(g.Skipped, fmt.Sprintf(
				"%s: no usable delta left; expression setup skipped for this character", c.Label))
			continue
		}
		g.targets[c] = targets
	}
	return nil
}

func (g *Gen) deltaTargets(c *panel.Char) []panel.Target {
	if g.targets == nil {
		return nil
	}
	return g.targets[c]
}

// chars returns the character table, rejecting the states that would generate a
// file that references resources nobody declared.
func (g *Gen) chars() ([]*panel.Char, error) {
	if err := g.prepare(); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	out := make([]*panel.Char, 0, len(g.T.Chars))
	for _, c := range g.T.Chars {
		if g.dropped[c] {
			continue
		}
		if seen[c.Cid] {
			return nil, fmt.Errorf("duplicate cid %d in the character table", c.Cid)
		}
		seen[c.Cid] = true
		if c.Pose && len(c.PvbPaths) == 0 {
			return nil, fmt.Errorf(
				"%s has pose=true but no pvb_paths -- neither a bake source nor a "+
					"write-back target, so the output would reference an empty "+
					"resource name (silent failure)", c.Label)
		}
		out = append(out, c)
	}
	return out, nil
}

// blkRouteRStateConsts declares the per-character route-R state.
//
// It lives in its own block because every declaration here expands per cid,
// which makes them part of the character data unit rather than the main body.
func (g *Gen) blkRouteRStateConsts() ([]string, error) {
	chars, err := g.chars()
	if err != nil {
		return nil, err
	}
	var out []string
	anyPose := false
	for _, c := range chars {
		if c.Pose {
			anyPose = true
			out = append(out, fmt.Sprintf("global $ep_r_base%d = 0", c.Cid))
		}
	}
	if anyPose {
		out = append(out, "")
	}
	for _, c := range chars {
		if c.Pose {
			out = append(out, fmt.Sprintf("global persist $ep_r_bake%d = 0", c.Cid))
			out = append(out, fmt.Sprintf("global persist $ep_r_off%d = 0", c.Cid))
		}
	}
	for _, c := range chars {
		if c.Pose && len(c.PoseExclude) > 0 {
			out = append(out, "global persist "+routeRSkipDbg+strconv.Itoa(c.Cid)+" = 0")
		}
	}
	for _, c := range chars {
		if c.Pose && len(c.ModDelta) > 0 {
			out = append(out, fmt.Sprintf("global $ep_md_baked%d = 0", c.Cid))
		}
	}
	for _, c := range chars {
		if c.Pose && len(c.ModDelta) > 0 {
			out = append(out, fmt.Sprintf("global $ep_bkt%d = -1", c.Cid))
		}
	}
	for _, c := range chars {
		if c.Pose && len(c.ModDelta) > 0 {
			out = append(out, "")
			break
		}
	}
	for _, c := range chars {
		if len(c.NativeScene.Gate) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("global $ep_nat_posed%d = 0", c.Cid))
		out = append(out, fmt.Sprintf("global $ep_nat_hits%d = 0", c.Cid))
		out = append(out, fmt.Sprintf("global $ep_off_ok%d = 0", c.Cid))
	}
	// `local` scope inside a command list is not observable offline, so the
	// "did this draw actually save a reference" flag is a global that is reset
	// explicitly on every entry. It carries no cid: it is written and read
	// inside one command-list run, and runs are serialised.
	out = append(out, "global $ep_nat_saved6 = 0")
	// Persisted diagnostics: the only way to tell "the section never ran" from
	// "it ran but had no effect" without looking at a face.
	for _, n := range []string{"ep_ndbg1", "ep_ndbg2", "ep_ndbg3", "ep_ndbg4",
		"ep_ndbg5", "ep_ndbg6", "ep_ndbg7"} {
		out = append(out, "global persist $"+n+" = 0")
	}
	// The declaration set must match the writers exactly. `ep_nh`/`ep_nl` are
	// written from the zero-mod anchor, so only adapted characters get them;
	// `ep_nw` is written from the value route, so every character gets it.
	// Declaring all of them would make a 0 ambiguous.
	for _, c := range g.T.Chars {
		if len(c.NativeScene.Gate) > 0 {
			out = append(out, fmt.Sprintf("global persist $ep_nh%d = 0", c.Cid))
			out = append(out, fmt.Sprintf("global persist $ep_nl%d = 0", c.Cid))
		}
		out = append(out, fmt.Sprintf("global persist $ep_nw%d = 0", c.Cid))
	}
	return out, nil
}

// blkValueRoute writes the shapekey values into each character mod's own value
// table and leaves everything else to that mod's native chain.
func (g *Gen) blkValueRoute() ([]string, error) {
	var out []string
	for _, c := range g.T.Chars {
		cap := c.Cap()
		secTo := "TextureOverrideEpWrite" + cap
		secCl := "CommandListEpWrite" + cap
		// The per-set `cs-u5` binding used to be expanded here; it now lives in
		// the user layer so that installing a mod and adding an emote never
		// touch the same file.
		secBind := "CommandListEpBindU5" + cap
		out = append(out, "["+secTo+"]")
		out = append(out, "hash = "+c.AnchorHash)
		out = append(out, fmt.Sprintf("match_priority = %d", valueRoutePrio))
		out = append(out, "if cs == "+skLoaderFilter)
		out = append(out, "    run = "+secCl)
		out = append(out, "endif")
		out = append(out, "")
		out = append(out, "["+secCl+"]")
		out = append(out, fmt.Sprintf("$ep_nw%d = $ep_nw%d + 1", c.Cid, c.Cid))
		out = append(out, fmt.Sprintf("x%d = $ep_native_off_%s", epShieldSlot, c.Name))
		out = append(out, "run = "+"CommandListEpU5Shield"+strconv.Itoa(c.Cid))
		// A character with a delta route must not also emit the recipe through
		// SetShapeKey: the two are two encodings of the same recipe, so turning
		// both on doubles the amplitude.
		if len(c.ModDelta) == 0 {
			for k := 0; k < g.K; k++ {
				out = append(out, fmt.Sprintf("if $ep_cid%d%d >= 0 && $ep_cw%d%d > 0", c.Cid, k, c.Cid, k))
				out = append(out, "    run = "+secBind)
				out = append(out, fmt.Sprintf("    $\\WWMIv1\\shapekey_id = $ep_cid%d%d", c.Cid, k))
				out = append(out, fmt.Sprintf("    $\\WWMIv1\\shapekey_value = $ep_cw%d%d", c.Cid, k))
				out = append(out, "    run = CommandList\\WWMIv1\\SetShapeKey")
				out = append(out, "endif")
			}
		}
		out = append(out, "")
		out = append(out, g.blkBindU5(c)...)
		out = append(out, "")
		out = append(out, g.blkU5Shield(c)...)
		out = append(out, "")
	}
	return out, nil
}

// blkBindU5 is the user layer's single expansion point for `sk_paths`. Its
// rows follow the installed mods and its calls follow the emote library, so
// keeping it in the user layer stops those two kinds of update from rewriting
// the same file.
func (g *Gen) blkBindU5(c *panel.Char) []string {
	cap := c.Cap()
	var out []string
	out = append(out, "[CommandListEpBindU5"+cap+"]")
	out = append(out, "cs-u5 = "+dummyU5)
	for _, p := range c.SkPaths {
		out = append(out, g.shims.bindU5(p))
	}
	return out
}

// blkU5Shield emits the "disable native expressions" pair (panel key 6).
//
// Two sections, mirroring the zero-mod family: a command list that binds the
// value table per mod, and a shader section that describes what to run. The
// split lets the command list be gated by `if` while the shader just executes.
//
// Finished-format sets (a mod that ships its own baked shape-key chain) are not
// supported by this build: their value tables are shielded but never written.
// They are reported to the user at configuration time instead.
func (g *Gen) blkU5Shield(c *panel.Char) []string {
	secCl := "CommandListEpU5Shield" + strconv.Itoa(c.Cid)
	secSh := "CustomShaderEpU5Shield" + strconv.Itoa(c.Cid)
	// The X dimension follows the D3D11 group size (64 threads), which is a
	// platform constant and not the same thing as the element count.
	dx := (epU5ShieldFloats + 63) / 64
	var out []string
	out = append(out, "["+secCl+"]")
	out = append(out, "    cs-u5 = "+dummyU5)
	out = append(out, "    run = "+secSh)
	for _, p := range c.SkPaths {
		out = append(out, "    "+g.shims.bindU5(p))
		out = append(out, "    run = "+secSh)
	}
	out = append(out, "")
	out = append(out, "["+secSh+"]")
	out = append(out, "cs = "+g.csRef(nativeU5ShieldHSL, secSh))
	out = append(out, "vs = null")
	out = append(out, "ps = null")
	out = append(out, "hs = null")
	out = append(out, "ds = null")
	out = append(out, "gs = null")
	out = append(out, fmt.Sprintf("dispatch = %d, 1, 1", dx))
	out = append(out, "cs-u5 = null")
	return out
}

// pathOf turns a `pose_exclude` / `pos_variants` ini-relative path into the
// fully-qualified resource path used by `pvb_paths`.
//
// The ini file name is itself one component of the path (the second to last),
// not something derived from the directory -- so the last component (the
// resource name) is dropped and nothing is appended.
func pathOf(c *panel.Char, rel string) (string, bool) {
	key := strings.TrimSpace(rel)
	// A namespace-declaring mod stores its pvb at Resource\<ns>\..., which the
	// path form below cannot reach. The scan layer writes that resource path
	// itself as the set key in this case, so a direct hit resolves the set to
	// its own pvb and it can be excluded from the bake route.
	for _, p := range c.PvbPaths {
		if strings.EqualFold(key, p) {
			return p, true
		}
	}
	want := strings.TrimPrefix(strings.ReplaceAll(key, "\\", "/"), "/")
	if strings.HasPrefix(strings.ToLower(want), "mods/") {
		want = want[len("mods/"):]
	}
	for _, p := range c.PvbPaths {
		parts := strings.Split(p, "\\")
		if len(parts) < 4 || parts[0] != "Resource" || parts[1] != "Mods" {
			continue
		}
		if strings.Join(parts[2:len(parts)-1], "/") == want {
			return p, true
		}
	}
	return "", false
}

type finishedSKTarget struct {
	Sk  string
	VCs []int
}

// finishedSKTargets returns the shape-key value tables of sets that opted out
// of the WWEmote bake, together with the vertex counts that identify them.
// Their meshes are already final, but the panel recipe still has to reach
// their own tables. SkPaths and PvbPaths are written in parallel by the scan
// layer, so a pose_exclude pvb identifies its matching sk path by index. The
// vertex-count guard is essential when a raw and a finished set declare the
// same namespace and therefore share one resource path: only the finished
// vertex count may take the direct SetShapeKey route, or the raw set receives
// the recipe twice.
func finishedSKTargets(c *panel.Char) []finishedSKTarget {
	excluded := map[string][]int{}
	for _, rel := range c.PoseExclude {
		p, ok := pathOf(c, rel)
		if !ok {
			continue
		}
		vc, ok := c.PoseExcludeVC[rel]
		if !ok {
			vc = c.PoseExcludeVC[p]
		}
		if vc <= 0 {
			continue
		}
		key := strings.ToLower(p)
		seen := false
		for _, v := range excluded[key] {
			if v == vc {
				seen = true
				break
			}
		}
		if !seen {
			excluded[key] = append(excluded[key], vc)
		}
	}
	if len(excluded) == 0 {
		return nil
	}
	bySK := map[string]int{}
	var out []finishedSKTarget
	for i, p := range c.PvbPaths {
		vcs := excluded[strings.ToLower(p)]
		if i >= len(c.SkPaths) || len(vcs) == 0 {
			continue
		}
		sk := strings.TrimSpace(c.SkPaths[i])
		if sk == "" {
			continue
		}
		if idx, ok := bySK[sk]; ok {
			out[idx].VCs = append(out[idx].VCs, vcs...)
			continue
		}
		bySK[sk] = len(out)
		out = append(out, finishedSKTarget{Sk: sk, VCs: append([]int(nil), vcs...)})
	}
	for i := range out {
		sort.Ints(out[i].VCs)
		dedup := out[i].VCs[:0]
		for _, v := range out[i].VCs {
			if len(dedup) == 0 || dedup[len(dedup)-1] != v {
				dedup = append(dedup, v)
			}
		}
		out[i].VCs = dedup
	}
	return out
}

// vcOfAll returns every vertex count that identifies a skipped set, sorted
// and de-duplicated. A character can legitimately claim more than one
// self-baked (finished) set, each with its own vertex count; the runtime skip
// dispatch must fire for any of them. Requiring exactly one (the old vcOf
// contract) made a second finished set ambiguous and dropped the whole
// dispatch, which cost every other set its pose apply.
func vcOfAll(c *panel.Char, rels []string) ([]int, error) {
	seen := map[int]bool{}
	var vals []int
	for _, r := range rels {
		if v, ok := c.PoseExcludeVC[r]; ok && !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		keys := make([]string, 0, len(c.PoseExcludeVC))
		for k := range c.PoseExcludeVC {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf(
			"%s: no skipped set has a known custom_vertex_count "+
				"(skip set has %d sets)\n    pose_exclude = %v\n    pose_exclude_vc = %v",
			c.Label, len(rels), rels, keys)
	}
	sort.Ints(vals)
	return vals, nil
}

func poseApplyCLName(cid int) string { return "CommandListEpPoseApply" + strconv.Itoa(cid) }
