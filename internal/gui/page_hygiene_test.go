package gui

import (
	"os"
	"strings"
	"testing"
)

// The result box used to be a fixed 54px slot with absolutely positioned
// children. A partial config then hid most of the failed-mod list, and the
// footer sat above empty space instead of the panel bottom. Keep the layout
// contract here so a later CSS tweak cannot silently reintroduce either one.
func TestEmbeddedPageReportLayoutContract(t *testing.T) {
	raw, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Join(strings.Fields(string(raw)), " ")
	for _, needle := range []string{
		".body{padding:10px 18px 10px;display:flex;flex-direction:column;min-height:0;overflow:hidden}",
		".result-slot{flex:1 1 auto;min-height:96px;",
		"display:flex;flex-direction:column;gap:8px;overflow-y:auto;",
		".summary.show{display:flex;flex-direction:column;gap:5px}",
		".summary>.fail{border-color:#f0c4cc;",
		".summary .detail{display:block;",
		"overflow-wrap:anywhere}",
		".foot{flex:none;display:flex;justify-content:flex-end;margin-top:auto;",
		// Vertical-only resize strips and the width-locked Go binding.
		".card>.resize-edge{position:absolute;left:0;right:0;height:6px;cursor:ns-resize",
		"id=\"resizeTop\"", "id=\"resizeBottom\"",
		"window.resizeHeight(active.edge",
		"window.commitWindowHeight()",
	} {
		if !strings.Contains(page, needle) {
			t.Errorf("embedded page lost layout contract %q", needle)
		}
	}
	// The program panel must never grow its own scrollbar; only the report box
	// scrolls. An overflow:auto back on .body reintroduces the side strip.
	if strings.Contains(page, ".body{padding:10px 18px 10px;display:flex;flex-direction:column;min-height:0;overflow:auto") {
		t.Error("program panel .body must be overflow:hidden, not auto")
	}
}

// The two option checkboxes and the vertical drag height are remembered
// across restarts. Keep the storage keys and the Go bindings present so a
// later tidy-up cannot silently drop the memory.
func TestEmbeddedPageMemoryContract(t *testing.T) {
	raw, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, needle := range []string{
		"\"wwemote-ignore-disabled\"",
		"\"wwemote-clean-stale\"",
		"localStorage.setItem(OPT_KEY_IGNORE",
		"localStorage.setItem(OPT_KEY_STALE",
		"restoreChecks()",
	} {
		if !strings.Contains(page, needle) {
			t.Errorf("embedded page lost checkbox-memory contract %q", needle)
		}
	}
}

type pageComment struct {
	where string
	line  int
	text  string
}

// scanComments walks the page with the same string-awareness a browser would
// need: a // inside a literal is not a comment, and CSS only knows /* */.
func scanComments(page string) []pageComment {
	var out []pageComment
	i, n := 0, len(page)
	line := 1
	quote := byte(0)
	jsMode := false

	record := func(where string, start int, text string) {
		out = append(out, pageComment{where: where, line: start, text: text})
	}
	countLines := func(s string) int { return strings.Count(s, "\n") }

	for i < n {
		// Toggle the scanner mode at the tag boundaries.
		if quote == 0 && strings.HasPrefix(page[i:], "<script") {
			if j := strings.Index(page[i:], ">"); j >= 0 {
				jsMode = true
				line += countLines(page[i : i+j+1])
				i += j + 1
				continue
			}
		}
		if quote == 0 && strings.HasPrefix(page[i:], "</script") {
			jsMode = false
			if j := strings.Index(page[i:], ">"); j >= 0 {
				line += countLines(page[i : i+j+1])
				i += j + 1
				continue
			}
		}

		c := page[i]
		if quote != 0 {
			if c == '\\' && i+1 < n {
				i += 2
				continue
			}
			if c == quote {
				quote = 0
			}
			if c == '\n' {
				line++
			}
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			i++
			continue
		}
		if strings.HasPrefix(page[i:], "<!--") {
			if j := strings.Index(page[i:], "-->"); j >= 0 {
				record("html", line, page[i+4:i+j])
				line += countLines(page[i : i+j+3])
				i += j + 3
				continue
			}
		}
		if strings.HasPrefix(page[i:], "/*") {
			if j := strings.Index(page[i:], "*/"); j >= 0 {
				where := "css"
				if jsMode {
					where = "js"
				}
				record(where, line, page[i+2:i+j])
				line += countLines(page[i : i+j+2])
				i += j + 2
				continue
			}
		}
		if jsMode && strings.HasPrefix(page[i:], "//") {
			j := strings.IndexByte(page[i:], '\n')
			if j < 0 {
				record("js", line, page[i+2:])
				break
			}
			record("js", line, page[i+2:i+j])
			i += j
			continue
		}
		if c == '\n' {
			line++
		}
		i++
	}
	return out
}
