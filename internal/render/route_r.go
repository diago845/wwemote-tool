package render

import (
	"fmt"
	"sort"
	"strings"

	"wwemote/internal/panel"
)

// blkRouteR emits the baking route: resources, anchor sections, the bake
// command list and the write-back. Baking makes the panel independent of the
// game's vertex-shader pick: the value route writes into the native shapekey
// offset buffer (read only via vb6), which a still frame's shader lacks, so
// baking writes the offset into the position stream instead.
func (g *Gen) blkRouteR() ([]string, error) {
	chars, err := g.chars()
	if err != nil {
		return nil, err
	}
	var out []string
	// Release builds do not bind the emergency key, so [KeyEpRouteR] is not
	// generated at all; $ep_r_on keeps its [Constants] initial value of 1.
	out = append(out, "")

	// ---- the "original mesh" pair, one per character -------------------
	for _, c := range chars {
		if !c.Pose {
			continue
		}
		cid := c.Cid
		out = append(out, fmt.Sprintf("[ResourceEpPosedSrc%d]", cid))
		out = append(out, "")
		out = append(out, fmt.Sprintf("[ResourceEpPosedRW%d]", cid))
		out = append(out, "type = RWBuffer")
		out = append(out, "format = R32_FLOAT")
		out = append(out, "")
		// Every body shape needs its own pair: all of them are computed in the
		// same frame, because the mod's own `$xxx_body_shape` cannot be read
		// from here. The shape index is independent of the bucket -- only one
		// bucket is active per frame -- so shapes of different buckets may
		// share a slot.
		nvar := 0
		for _, d := range c.ModDelta {
			if d != nil && len(d.Pos) > nvar {
				nvar = len(d.Pos)
			}
		}
		if len(c.ModDelta) == 0 {
			// Non-delta characters get their shapes from `pos_variants`, and
			// slots are numbered continuously across sets.
			nvar = 1
			for _, v := range c.PosVariants {
				if len(v)-1 > 0 {
					nvar += len(v) - 1
				}
			}
		}
		for i := 1; i < nvar; i++ {
			out = append(out, fmt.Sprintf("[ResourceEpPosedSrc%d_%d]", cid, i))
			out = append(out, "")
			out = append(out, fmt.Sprintf("[ResourceEpPosedRW%d_%d]", cid, i))
			out = append(out, "type = RWBuffer")
			out = append(out, "format = R32_FLOAT")
			out = append(out, "")
		}
	}

	// ---- the anchor, the bake command list and the vb6 clear -----------
	for _, c := range chars {
		if !c.Pose {
			continue
		}
		cid := c.Cid
		paths := c.PvbPaths
		// A set whose pose_exclude/variant entry cannot be mapped to a real
		// pvb path is dropped on its own, with a reason the caller reports.
		// Failing the whole render here would cost every other set its config
		// (the same one-bad-set-takes-all bug fixed in scan.UserLayer). A mod
		// using a namespace has pvb paths of the form Resource\<ns>\..., which
		// pathOf deliberately does not treat as a bake target; such a set must
		// not be skipped from a path it never had.
		skipP := map[string]bool{}
		keptExclude := make([]string, 0, len(c.PoseExclude))
		for _, rel := range c.PoseExclude {
			want, ok := pathOf(c, rel)
			if !ok {
				g.Skipped = append(g.Skipped, fmt.Sprintf(
					"%s: pose_exclude entry %q has no matching pvb path; skipping that set",
					c.Label, rel))
				continue
			}
			skipP[want] = true
			keptExclude = append(keptExclude, rel)
		}
		// What the bake body may write back / restore is "every set except the
		// skipped ones". Skipping means the four steps are all skipped; a set
		// that bakes itself would otherwise be overwritten with the raw mesh
		// and lose its expressions permanently, with no error.
		var bakePaths []string
		for _, p := range paths {
			if !skipP[p] {
				bakePaths = append(bakePaths, p)
			}
		}
		// A delta character has exactly one vertex space, so the R chain must
		// only touch that one -- otherwise A's mesh is written into B.
		var mdt []panel.Target
		if len(c.ModDelta) > 0 {
			// chars() already ran the same filter used by the other output
			// paths, so every consumer sees the same usable bucket list.
			mdt = g.deltaTargets(c)
		}
		if len(mdt) > 0 {
			// A delta whose write-back target is also in pose_exclude is a
			// contradiction for that one set: "bake it" and "do not bake it".
			// Drop just that delta target (honouring the skip) instead of
			// failing the whole render; the skip set already opted out of the
			// tool's bake, so leaving it alone is the safe reading.
			// The collision must be by VERTEX COUNT, not by path alone: a raw
			// set and a finished set can declare the same namespace and thus
			// the same pvb path while being different meshes (a0e72 raw
			// vc=75749 vs hsinnv finished vc=269832). The skip belongs to the
			// finished one; the raw one's delta must survive.
			kept := make([]panel.Target, 0, len(mdt))
			for _, t := range mdt {
				collide := false
				for _, p := range t.Paths {
					if !skipP[p] {
						continue
					}
					if svc, ok := c.PoseExcludeVC[p]; ok {
						if svc == t.VC {
							collide = true
							break
						}
					} else {
						// No vc recorded for that skip key: keep the safe
						// path-only behaviour.
						collide = true
						break
					}
				}
				if collide {
					g.Skipped = append(g.Skipped, fmt.Sprintf(
						"%s: delta for %q also in pose_exclude; dropped that set's bake",
						c.Label, t.Paths[0]))
					continue
				}
				kept = append(kept, t)
			}
			mdt = kept
			mdPaths := make([]string, 0, len(mdt))
			for _, t := range mdt {
				mdPaths = append(mdPaths, t.Paths[0])
			}
			paths = mdPaths
			bakePaths = mdPaths
		}
		// Body shapes of a non-delta character: each shape is a separate mesh
		// with its own position stream, so each is computed and written back on
		// its own. Slot numbers are continuous across sets -- restarting per set
		// would write A's baked mesh into B's resource.
		type varSlot struct {
			Slot int
			Path string
		}
		var varPlan []varSlot
		if len(mdt) == 0 && len(c.PosVariants) > 0 {
			relOf := map[string][]string{}
			keys := make([]string, 0, len(c.PosVariants))
			for rel := range c.PosVariants {
				keys = append(keys, rel)
			}
			sort.Strings(keys)
			for _, rel := range keys {
				dp, ok := pathOf(c, rel)
				if !ok {
					g.Skipped = append(g.Skipped, fmt.Sprintf(
						"%s: pos_variants entry %q has no matching pvb path; skipping that set's variants",
						c.Label, rel))
					continue
				}
				relOf[dp] = c.PosVariants[rel]
			}
			slot := 0
			for _, p := range bakePaths {
				items := relOf[p]
				if len(items) == 0 {
					continue
				}
				for _, vp := range items[1:] {
					slot++
					varPlan = append(varPlan, varSlot{slot, vp})
				}
			}
		}
		nm := c.Cap()
		out = append(out, "[TextureOverrideEpPose"+nm+"]")
		out = append(out, "hash = "+c.AnchorHash)
		out = append(out, "match_priority = 2")
		out = append(out, "if cs == "+skMultFilter)
		out = append(out, "    run = CommandListEpPose"+nm)
		out = append(out, "endif")
		out = append(out, "")
		out = append(out, "[CommandListEpPose"+nm+"]")
		if len(skipP) > 0 {
			vcSkips, vcErr := vcOfAll(c, keptExclude)
			if vcErr != nil {
				// No skipped set has a known vertex count: the runtime dispatch
				// has no trigger. Keep skipP so these sets stay out of the bake
				// capture, but omit the dispatch guard instead of failing the
				// whole render.
				g.Skipped = append(g.Skipped, fmt.Sprintf(
					"%s: cannot pick a skip vertex count (%v); omitting the skip dispatch",
					c.Label, vcErr))
			} else {
				// A skipped set needs to be recognised by the vertex count the
				// mod's multiplier callback publishes. More than one finished
				// set may be claimed at once (each with its own vc), so the
				// dispatch fires for ANY of them via ||. The arms are exclusive:
				// a trigger landing on a skipped set must not run the restore
				// copies, which would undo that set's own baked mesh.
				conds := make([]string, 0, len(vcSkips))
				for _, v := range vcSkips {
					conds = append(conds, fmt.Sprintf("$\\WWMIv1\\custom_vertex_count == %d", v))
				}
				out = append(out, "if "+strings.Join(conds, " || "))
				// This guard only keeps the restore copies off a skipped set's
				// own mesh; it does not publish `ep_bkt<cid>`. Finished-format
				// sets are not supported by this build, so the panel gate must
				// stay closed for them.
				out = append(out, fmt.Sprintf("    %s%d = 1", routeRSkipDbg, cid))
				out = append(out, "else")
				out = append(out, "    run = "+poseApplyCLName(cid))
				out = append(out, "endif")
				out = append(out, "")
				out = append(out, "["+poseApplyCLName(cid)+"]")
			}
		}
		out = append(out, "if $ep_r_on")
		out = append(out, "    if $\\WWMIv1\\custom_vertex_count > 0")
		out = append(out, fmt.Sprintf("        $ep_r_bake%d = 1", cid))
		if len(c.ModDelta) > 0 {
			out = append(out, fmt.Sprintf("        $ep_md_baked%d = 1", cid))
		}
		if len(c.ModDelta) > 0 {
			// Delta characters read the base mesh from disk instead of the
			// position buffer: the "capture once" flag is a non-persistent
			// global, and after a reload the buffer already carries the delta,
			// so a second capture would add the offset twice and tear the mesh.
			out = append(out, fmt.Sprintf("        $ep_bkt%d = -1", cid))
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("        if $\\WWMIv1\\custom_vertex_count == %d", t.VC))
				out = append(out, fmt.Sprintf("            $ep_bkt%d = %d", cid, t.VC))
				for _, s := range panel.PosSlots(t.Pos, t.Base) {
					out = append(out, fmt.Sprintf("            ResourceEpPosedSrc%d%s = "+
						"copy ResourceEpModBase%d_%d%s", cid, s.Suffix, cid, t.VC, s.Suffix))
				}
				out = append(out, "        endif")
			}
			out = append(out, fmt.Sprintf("        $ep_r_base%d = 1", cid))
		} else {
			out = append(out, fmt.Sprintf("        if $ep_r_base%d == 0", cid))
			for _, p := range paths {
				out = append(out, "            "+g.shims.copySrc(
					fmt.Sprintf("ResourceEpPosedSrc%d", cid), p))
			}
			for _, v := range varPlan {
				out = append(out, "            "+g.shims.copySrc(
					fmt.Sprintf("ResourceEpPosedSrc%d_%d", cid, v.Slot), v.Path))
			}
			out = append(out, fmt.Sprintf("            $ep_r_base%d = 1", cid))
			out = append(out, "        endif")
		}
		out = append(out, fmt.Sprintf("        ResourceEpPosedRW%d = copy ResourceEpPosedSrc%d", cid, cid))
		out = append(out, fmt.Sprintf("        cs-t6 = ResourceEpPosedSrc%d", cid))
		out = append(out, fmt.Sprintf("        cs-u6 = ResourceEpPosedRW%d", cid))
		out = append(out, "        run = CustomShader\\WWMIv1\\ShapeKeyApplier")
		for _, v := range varPlan {
			out = append(out, fmt.Sprintf("        ResourceEpPosedRW%d_%d = copy ResourceEpPosedSrc%d_%d",
				cid, v.Slot, cid, v.Slot))
			out = append(out, fmt.Sprintf("        cs-t6 = ResourceEpPosedSrc%d_%d", cid, v.Slot))
			out = append(out, fmt.Sprintf("        cs-u6 = ResourceEpPosedRW%d_%d", cid, v.Slot))
			out = append(out, "        run = CustomShader\\WWMIv1\\ShapeKeyApplier")
		}
		varVcs := map[int][]int{}
		for _, t := range mdt {
			slots := panel.PosSlots(t.Pos, t.Base)
			for i := 1; i < len(slots); i++ {
				varVcs[i] = append(varVcs[i], t.VC)
			}
		}
		for _, i := range sortedKeys(varVcs) {
			for _, vc := range varVcs[i] {
				out = append(out, fmt.Sprintf("        if $ep_bkt%d == %d", cid, vc))
				out = append(out, fmt.Sprintf("            ResourceEpPosedRW%d_%d = copy ResourceEpPosedSrc%d_%d",
					cid, i, cid, i))
				out = append(out, fmt.Sprintf("            cs-t6 = ResourceEpPosedSrc%d_%d", cid, i))
				out = append(out, fmt.Sprintf("            cs-u6 = ResourceEpPosedRW%d_%d", cid, i))
				out = append(out, "            run = CustomShader\\WWMIv1\\ShapeKeyApplier")
				out = append(out, "        endif")
			}
		}
		if len(mdt) > 0 {
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("        if $ep_bkt%d == %d", cid, t.VC))
				for _, s := range panel.PosSlots(t.Pos, t.Base) {
					targets := t.Paths
					if s.Writeback != "" {
						targets = []string{s.Writeback}
					}
					for _, p := range targets {
						out = append(out, fmt.Sprintf("            %s = copy ResourceEpPosedRW%d%s", p, cid, s.Suffix))
					}
				}
				out = append(out, "        endif")
			}
		} else {
			for _, p := range bakePaths {
				out = append(out, fmt.Sprintf("        %s = copy ResourceEpPosedRW%d", p, cid))
			}
			for _, v := range varPlan {
				out = append(out, fmt.Sprintf("        %s = copy ResourceEpPosedRW%d_%d", v.Path, cid, v.Slot))
			}
		}
		out = append(out, "    endif")
		out = append(out, "else")
		out = append(out, fmt.Sprintf("    $ep_r_off%d = 1", cid))
		out = append(out, fmt.Sprintf("    if $ep_r_base%d == 1", cid))
		if len(mdt) > 0 {
			// Restore by the bucket the snapshot was taken from, not by the
			// current vertex count: the snapshot holds one set's mesh, and
			// writing it into another set destroys that set permanently.
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("        if $ep_bkt%d == %d", cid, t.VC))
				for _, s := range panel.PosSlots(t.Pos, t.Base) {
					targets := t.Paths
					if s.Writeback != "" {
						targets = []string{s.Writeback}
					}
					for _, p := range targets {
						out = append(out, fmt.Sprintf("            %s = copy ResourceEpPosedSrc%d%s", p, cid, s.Suffix))
					}
				}
				out = append(out, "        endif")
			}
		} else {
			for _, p := range bakePaths {
				out = append(out, fmt.Sprintf("        %s = copy ResourceEpPosedSrc%d", p, cid))
			}
			for _, v := range varPlan {
				out = append(out, fmt.Sprintf("        %s = copy ResourceEpPosedSrc%d_%d", v.Path, cid, v.Slot))
			}
		}
		out = append(out, fmt.Sprintf("        $ep_r_base%d = 0", cid))
		out = append(out, "    endif")
		out = append(out, "endif")
		out = append(out, "")
		// vb6 must be cleared in a draw context; clearing it in compute does
		// nothing. It is generated unconditionally: "this mod clears vb6 itself"
		// is a per-set property, so keying on it silently dropped the section
		// for the other sets and produced geometric distortion.
		out = append(out, "[TextureOverrideEpVb6"+nm+"]")
		out = append(out, "hash = "+c.BodyHash)
		out = append(out, "match_priority = -1")
		out = append(out, fmt.Sprintf("if $ep_chr_%s <= 0", c.Name))
		out = append(out, "    $ep_chr_n = $ep_chr_n + 1")
		out = append(out, "endif")
		out = append(out, fmt.Sprintf("$ep_chr_%s = 1", c.Name))
		out = append(out, "$ep_chr_any = 1")
		out = append(out, "if $ep_r_on")
		out = append(out, "    $ep_ndbg1 = $ep_ndbg1 + 1")
		// The clear also needs "the panel actually owns this mesh": a mod that
		// is installed but not adapted has no bucket, so killing vb6 would drop
		// the game's own offsets and lose native expressions as well.
		out = append(out, fmt.Sprintf("    if $ep_hasmod_%s > 0", c.Name))
		out = append(out, fmt.Sprintf("        if $ep_bkt%d > 0", cid))
		out = append(out, "            vb6 = null")
		out = append(out, "        endif")
		out = append(out, "    endif")
		out = append(out, "endif")
		out = append(out, "")

		// ---- the delta route ------------------------------------------
		if len(mdt) > 0 {
			nmD := c.Cap()
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("[%s%d_%d]", userDeltaSectLoc, cid, t.VC))
				out = append(out, "type = Buffer")
				out = append(out, "format = DXGI_FORMAT_R32_FLOAT")
				out = append(out, "stride = 4")
				out = append(out, "filename = "+t.Sparse)
				out = append(out, "")
				// The position buffer is R32G32B32 (96 bit), which DX11 refuses
				// to expose as a UAV, so the read-modify-write goes through an
				// R32_FLOAT buffer: copy in, dispatch, copy back.
				out = append(out, fmt.Sprintf("[ResourceEpModRW%d_%d]", cid, t.VC))
				out = append(out, "type = RWBuffer")
				out = append(out, "format = R32_FLOAT")
				out = append(out, "stride = 12")
				out = append(out, fmt.Sprintf("array = %d", t.VC))
				out = append(out, "")
				// The base mesh is read-only and exists only as the capture
				// source for the anchor: the capture flag is non-persistent, so
				// after a reload the position buffer already carries the delta.
				out = append(out, fmt.Sprintf("[ResourceEpModBase%d_%d]", cid, t.VC))
				out = append(out, "type = Buffer")
				out = append(out, "format = DXGI_FORMAT_R32G32B32_FLOAT")
				out = append(out, "stride = 12")
				out = append(out, "filename = "+t.Base)
				out = append(out, "")
				for i, ps := range t.Pos {
					if i == 0 {
						continue
					}
					out = append(out, fmt.Sprintf("[ResourceEpModRW%d_%d_%d]", cid, t.VC, i))
					out = append(out, "type = RWBuffer")
					out = append(out, "format = R32_FLOAT")
					out = append(out, "stride = 12")
					out = append(out, fmt.Sprintf("array = %d", t.VC))
					out = append(out, "")
					out = append(out, fmt.Sprintf("[ResourceEpModBase%d_%d_%d]", cid, t.VC, i))
					out = append(out, "type = Buffer")
					out = append(out, "format = DXGI_FORMAT_R32G32B32_FLOAT")
					out = append(out, "stride = 12")
					out = append(out, "filename = "+ps.Base)
					out = append(out, "")
				}
			}
			out = append(out, "[TextureOverrideEpModDelta"+nmD+"]")
			out = append(out, "hash = "+c.BodyHash)
			out = append(out, "match_priority = -1")
			out = append(out, "if $ep_r_on > 0")
			out = append(out, "    if $ep_rcp_src >= 0 || $ep_clear_hold > 0")
			out = append(out, "        run = CommandListEpModDelta"+nmD)
			out = append(out, "    endif")
			out = append(out, "endif")
			out = append(out, "")
			out = append(out, "[CommandListEpModDelta"+nmD+"]")
			out = append(out, "    local $bak_x0 = x0")
			out = append(out, "    local $bak_y0 = y0")
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("    if $ep_bkt%d == %d", cid, t.VC))
				// The comment has to stay ASCII: the fragment may be a Chinese
				// directory name and shipped comments are ASCII-only.
				out = append(out, fmt.Sprintf("        ; ---- bucket vc=%d ----", t.VC))
				slots := panel.PosSlots(t.Pos, t.Base)
				out = append(out, fmt.Sprintf("        ResourceEpModRW%d_%d = copy ResourceEpPosedRW%d", cid, t.VC, cid))
				out = append(out, "        x0 = 0")
				out = append(out, fmt.Sprintf("        y0 = %d", t.VC))
				// The recipe channel is written in the outer list on purpose:
				// writing it inside the shader section gets the whole chain
				// optimised out, because the optimiser cannot see a compute
				// dispatch's side effects on a UAV.
				for k := 0; k < g.K; k++ {
					out = append(out, fmt.Sprintf("        x%d = $ep_cid%d%d", epRecipeBase+k, cid, k))
					out = append(out, fmt.Sprintf("        y%d = $ep_cw%d%d", epRecipeBase+k, cid, k))
				}
				out = append(out, fmt.Sprintf("        cs-t8 = %s%d_%d", userDeltaSectLoc, cid, t.VC))
				out = append(out, fmt.Sprintf("        cs-u6 = ResourceEpModRW%d_%d", cid, t.VC))
				out = append(out, fmt.Sprintf("        run = CustomShaderEpModSparse%d_%d", cid, t.VC))
				out = append(out, "        cs-t8 = null")
				out = append(out, "        cs-u6 = null")
				out = append(out, "        post x0 = $bak_x0")
				out = append(out, "        post y0 = $bak_y0")
				p0 := t.Paths
				if slots[0].Writeback != "" {
					p0 = []string{slots[0].Writeback}
				}
				for _, p := range p0 {
					out = append(out, fmt.Sprintf("        %s = copy ResourceEpModRW%d_%d", p, cid, t.VC))
				}
				for si, s := range slots {
					if si == 0 {
						continue
					}
					out = append(out, fmt.Sprintf("        ResourceEpModRW%d_%d%s = "+
						"copy ResourceEpPosedRW%d%s", cid, t.VC, s.Suffix, cid, s.Suffix))
					out = append(out, fmt.Sprintf("        cs-t8 = %s%d_%d", userDeltaSectLoc, cid, t.VC))
					out = append(out, fmt.Sprintf("        cs-u6 = ResourceEpModRW%d_%d%s", cid, t.VC, s.Suffix))
					out = append(out, fmt.Sprintf("        run = CustomShaderEpModSparse%d_%d", cid, t.VC))
					out = append(out, "        cs-t8 = null")
					out = append(out, "        cs-u6 = null")
					targets := t.Paths
					if s.Writeback != "" {
						targets = []string{s.Writeback}
					}
					for _, p := range targets {
						out = append(out, fmt.Sprintf("        %s = copy ResourceEpModRW%d_%d%s", p, cid, t.VC, s.Suffix))
					}
				}
				out = append(out, "    endif")
			}
			out = append(out, "")
			for _, t := range mdt {
				out = append(out, fmt.Sprintf("[CustomShaderEpModSparse%d_%d]", cid, t.VC))
				// The prefix must follow the section's real location: the user
				// layer sits one level down, so a hard-coded `.\Shaders\` would
				// not resolve and the section would be dropped silently.
				out = append(out, "cs = "+g.csRef("Shaders\\ep_mod_delta_sparse.hlsl",
					fmt.Sprintf("CustomShaderEpModSparse%d_%d", cid, t.VC)))
				out = append(out, fmt.Sprintf("dispatch = %d/64+1, 1, 1", t.VC))
				out = append(out, "")
			}
			out = append(out, "")
		}
	}
	return out, nil
}

func sortedKeys(m map[int][]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
