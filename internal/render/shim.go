package render

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strings"
)

// A namespace shim lets the user layer reference a resource whose path has a
// space (or similar) without writing that path into a value position. 3DMigoto
// splits values on spaces, but a file that declares `namespace = <path>` may
// use a short name that is completed against that namespace. The shim file is
// written next to the main user-layer ini and is picked up by include_recursive.
//
// Nothing here changes the output for mods whose paths need no shim: the
// rewrite is only applied to resource names that actually contain a breaking
// character, and the golden tests pin the bytes of the ordinary path.

const (
	shimFilePrefix = "_wwemote_ns_"
	shimCLPrefix   = "WWEmoteShim"
)

// shimFile is one generated shim: a namespace and the command lists that use
// short names against it.
type shimFile struct {
	ns      string // namespace value, e.g. Mods\Some Mod\mod.ini
	entries []shimEntry
}

type shimEntry struct {
	cl    string   // short command-list name inside the shim file
	lines []string // body (dst is a full, space-free name; src is a short name)
}

// shimSet collects everything RenderMods needs during one pass. It is reset at
// the start of each render so calling RenderMods twice is idempotent.
type shimSet struct {
	byNS  map[string]*shimFile
	order []string
	seq   int
}

func newShimSet() *shimSet {
	return &shimSet{byNS: map[string]*shimFile{}}
}

func badShimChar(s string) bool {
	return strings.ContainsAny(s, " \t]")
}

// splitShimResource turns a full custom-resource name into the namespace and
// short name a shim needs. It returns ok=false for names that 3DMigoto can
// already parse (no breaking character), so the caller leaves those untouched.
//
//	Resource\Mods\Some Mod\mod.ini\CustomShapeKeyValuesRW
//	  -> ns = "Mods\Some Mod\mod.ini", short = "ResourceCustomShapeKeyValuesRW"
func splitShimResource(full string) (ns, short string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(full), "resource\\") {
		return "", "", false
	}
	if !badShimChar(full) {
		return "", "", false
	}
	body := full[len("resource\\"):]
	i := strings.LastIndex(body, "\\")
	if i < 0 || i == 0 || i == len(body)-1 {
		return "", "", false
	}
	ns, tail := body[:i], body[i+1:]
	short = tail
	if !strings.HasPrefix(strings.ToLower(tail), "resource") {
		short = "Resource" + tail
	}
	return ns, short, true
}

// addCL appends a command list to the shim for ns and returns its full name as
// it will be registered by 3DMigoto (which the main ini uses in `run =`).
func (s *shimSet) addCL(ns string, lines []string) string {
	cl := fmt.Sprintf("%s%d", shimCLPrefix, s.seq)
	s.seq++
	f, ok := s.byNS[ns]
	if !ok {
		f = &shimFile{ns: ns}
		s.byNS[ns] = f
		s.order = append(s.order, ns)
	}
	f.entries = append(f.entries, shimEntry{cl: cl, lines: lines})
	return `CommandList\` + ns + `\` + cl
}

// bindU5 returns the line that binds sk as a shader resource. For a normal sk
// this is the original `cs-u5 = <name>`; for a breaking path it is a `run =`
// into a shim command list that binds the short name inside the mod namespace.
func (s *shimSet) bindU5(sk string) string {
	ns, short, ok := splitShimResource(sk)
	if !ok {
		return "cs-u5 = " + sk
	}
	cl := s.addCL(ns, []string{"cs-u5 = " + short})
	return "run = " + cl
}

// copySrc returns the line that copies src into dst. Only the src can be a
// breaking path (dst is a key and 3DMigoto reads the whole left-hand side).
// For a breaking src the copy moves into a shim: inside it the src is a short
// name completed against the mod namespace, while dst stays a full, space-free
// name that resolves back to the main ini's resource.
func (s *shimSet) copySrc(dst, src string) string {
	ns, short, ok := splitShimResource(src)
	if !ok {
		return dst + " = copy " + src
	}
	cl := s.addCL(ns, []string{dst + " = copy " + short})
	return "run = " + cl
}

// fileNames returns the shim file names with content, in a stable order. The
// name is derived from the namespace so an unchanged scope rewrites the same
// files byte-for-byte (WriteIfChanged then leaves the disk untouched).
func (s *shimSet) files() []struct{ Name, Text string } {
	nss := append([]string(nil), s.order...)
	sort.Strings(nss)
	out := make([]struct{ Name, Text string }, 0, len(nss))
	for _, ns := range nss {
		f := s.byNS[ns]
		sum := md5.Sum([]byte(ns))
		name := fmt.Sprintf("%s%x.ini", shimFilePrefix, sum[:6])
		var b strings.Builder
		b.WriteString("; WWEmote namespace shim - generated, do not edit\n")
		b.WriteString("; Short names in the command lists below resolve into the\n")
		b.WriteString("; mod whose path cannot be written in a value position.\n")
		b.WriteString("namespace = " + ns + "\n")
		for _, e := range f.entries {
			b.WriteString("\n[CommandList" + e.cl + "]\n")
			for _, ln := range e.lines {
				b.WriteString(ln + "\n")
			}
		}
		out = append(out, struct{ Name, Text string }{name, b.String()})
	}
	return out
}

// hasShim reports whether any resource needed a shim this pass.
func (s *shimSet) hasShim() bool { return len(s.order) > 0 }
