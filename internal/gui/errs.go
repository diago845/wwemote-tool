package gui

import "errors"

// uiErr is the two-line message a failed configuration shows the user.
//
// The error box only fits two lines of 11px text, so what/how are the only two
// strings rendered there: one conclusion in the user's own words, then the one
// action that fixes it. det (the raw cause) is never shown in the box; Error()
// returns it so the same call that reports the failure also appends the
// technical detail to the log, which is what a Diagnostics bundle carries.
type uiErr struct {
	what string
	how  string
	det  string
}

func (e *uiErr) Error() string {
	if e.det != "" {
		return e.det
	}
	return e.what
}

// bad builds a two-line refusal. how is empty when there is nothing the user
// can act on; det is empty when the conclusion says everything.
func bad(what, how, det string) *uiErr {
	return &uiErr{what: what, how: how, det: det}
}

// adviceReport is the standing next step for causes the user cannot fix alone:
// the panel's own numbers are inconsistent, so the author needs the bundle.
func adviceReport() string {
	return T("点左下角 Diagnostics 生成诊断报告，发给作者（已去除本机路径）。",
		"Click Diagnostics at the bottom left and send the report to the author (local paths removed).")
}

// twoLine renders a refusal for the error box before the run has started: no
// "setup failed" skeleton (nothing ran yet), just the conclusion and the next
// step. Unknown error shapes fall back to their own message.
func twoLine(err error) string {
	var ue *uiErr
	if errors.As(err, &ue) {
		msg := ue.what
		if ue.how != "" {
			msg += "\n" + ue.how
		}
		return msg
	}
	return err.Error()
}
