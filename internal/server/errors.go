package server

import (
	"errors"

	"patmonitor/internal/config"
)

// The refusal reasons the pages show, as **codes**.
//
// **They used to be sentences, and the pages showed them as they came.**
// `auth.js` did `show(body.error || T('auth.failed'))`: the catalogue was
// already there and was the fallback, while the real sentence arrived from the
// server in the language of whoever wrote the program. With the interface
// translated the result would have been an English form answering the first
// error in another language.
//
// The direction is the usual one — codes cross the API, the words live at the
// edges — and here the edge is the page, which is also the only one that knows
// what language it is speaking: the same answer is read by one browser and
// another.
//
// **The JSON field stays `error`**, so the shape of the response does not
// change: what changes is what is inside it. Whoever shows it does
// `T('err.' + code)`.
type errCode string

const (
	// The monitor has no password yet: it is not a refusal, it is a first start
	// that was never finished.
	ErrNoPassword errCode = "no-password"
	// The session is absent or expired.
	ErrNoSession errCode = "no-session"
	// The request could not be interpreted.
	ErrBadRequest errCode = "bad-request"
	// The configuration could not be written.
	ErrSaveFailed errCode = "save-failed"
	// Remote access is not compiled into this binary.
	ErrRemoteUnavailable errCode = "remote-unavailable"
	// The password is not the right one.
	ErrWrongPassword errCode = "wrong-password"
	// The current password, asked for in order to change it, is not the right
	// one.
	ErrWrongCurrentPassword errCode = "wrong-current-password"
	// The session could not be created.
	ErrSessionFailed errCode = "session-failed"
	// The two passwords typed do not match.
	ErrMismatch errCode = "mismatch"
	// The password is already set: `/setup` is the first start, not a change.
	ErrAlreadySet errCode = "already-set"
	// The chosen password is too short.
	ErrPasswordTooShort errCode = "password-too-short"
	// Exposing the monitor was asked for with no password.
	ErrNoPasswordForFunnel errCode = "no-password-for-funnel"
	// Too many attempts in a row: there is a wait. The seconds are carried by
	// `retryAfter`, because a duration written inside the sentence belongs to
	// one language as much as the words do.
	ErrTooMany errCode = "too-many"
	// The first configuration was asked for from somewhere other than the PC
	// the monitor runs on. See "administrative commands are given from in front
	// of the machine".
	ErrSetupNotThisPC errCode = "setup-not-this-pc"
	// The credentials did not arrive from one of our own pages. It is the
	// answer to a form submitted by somebody else's site — see
	// "credentials are taken only from our own pages".
	ErrCrossSite errCode = "cross-site"
	// The clip asked for is not there: a name that is not a name, or a file
	// deleted between the listing and the click.
	ErrNoSuchClip errCode = "no-such-clip"
	// The clip could not be touched. On Windows renaming or deleting an open
	// file does not work, and while somebody is downloading it the file is
	// open: whoever pressed has to know they can try again.
	ErrClipBusy errCode = "clip-busy"
	// The clips folder could not be read.
	ErrClipUnreadable errCode = "clip-unreadable"
	// The chosen microphone is not among the active ones: unplugged between the
	// listing and the click, or an ID that never existed.
	ErrNoSuchMic errCode = "no-such-microphone"
	// The microphones could not be listed.
	ErrMicListFailed errCode = "mic-list-failed"
	// The chosen camera is not among the connected ones: unplugged between the
	// listing and the click, or a link that never existed.
	ErrNoSuchCam errCode = "no-such-camera"
	// The cameras could not be listed.
	ErrCamListFailed errCode = "cam-list-failed"
	// There is nothing to record yet: the pre-roll ring is empty because the
	// first keyframe has not arrived. It lasts a few seconds after the start or
	// after a capture restart, and passes by itself.
	ErrNothingToRecord errCode = "nothing-to-record"
	// The recorder is not attached to this server. Like `remote-unavailable`:
	// it is not a refusal, it is a piece that is not here — the tests see it,
	// because they do not build the recorder.
	ErrRecordUnavailable errCode = "record-unavailable"
)

// AllErrCodes is the authoritative list, for the guard that demands a word in
// every catalogue: a code with no entry would show **as a code**, and no
// compiler would say so.
func AllErrCodes() []string {
	return []string{
		string(ErrNoPassword), string(ErrNoSession), string(ErrBadRequest),
		string(ErrSaveFailed), string(ErrRemoteUnavailable),
		string(ErrWrongPassword), string(ErrWrongCurrentPassword),
		string(ErrSessionFailed), string(ErrMismatch), string(ErrAlreadySet),
		string(ErrPasswordTooShort), string(ErrNoPasswordForFunnel),
		string(ErrTooMany),
		string(ErrSetupNotThisPC), string(ErrCrossSite),
		string(ErrNoSuchClip), string(ErrClipBusy), string(ErrClipUnreadable),
		string(ErrNoSuchMic), string(ErrMicListFailed),
		string(ErrNoSuchCam), string(ErrCamListFailed),
		string(ErrNothingToRecord), string(ErrRecordUnavailable),
	}
}

// codeFor turns an error from `internal/config` into its code.
//
// **It is recognised with `errors.Is`, not by comparing the message.** It is
// the same rule these codes exist for: a comparison on the sentence switches
// itself off the day the sentence changes, and it does so with no error
// anywhere — here it would mean a password that is too short refused with
// "invalid request".
//
// An error we do not recognise becomes an invalid request and **is logged**: it
// is the branch that must not be allowed to stay silent, because it means
// somebody added a refusal and did not name it here.
func (s *Server) codeFor(err error) errCode {
	switch {
	case errors.Is(err, config.ErrPasswordTooShort):
		return ErrPasswordTooShort
	case errors.Is(err, config.ErrNoPassword):
		return ErrNoPasswordForFunnel
	default:
		s.log.Error("unnamed refusal: the page will show a generic message", "error", err)
		return ErrBadRequest
	}
}
