module wwemote

go 1.27

require (
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	golang.org/x/sys v0.0.0-20210218145245-beda7e5e158e
)

require github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect

// Local fork: suppress the library's premature ShowWindow (white flash).
// Only change: webview.go CreateWithOptions no longer shows/updates/focuses
// the native window; the host reveals it after the first page has rendered.
replace github.com/jchv/go-webview2 => ./third_party/go-webview2
