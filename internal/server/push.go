package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"patmonitor/internal/push"
)

// The notification routes, one per thing the page's row does: fetch the key
// it subscribes with, hand over the subscription, take it back, ask for a
// test, and — from the service worker — say a notification was shown.
//
// **They are behind a session like everything else**, including the receipt:
// a service worker's fetch to its own origin carries the cookie, and a route
// that anybody could post ids to would let anybody write lines in the log.

// pushBody is what the page posts: the subscription as `PushSubscription`
// serialises it, plus the language it is speaking.
type pushBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Lang string `json:"lang"`
	ID   string `json:"id"`
	// Replaces is the subscription this one takes the place of, which the
	// service worker names when the browser has renewed it on its own.
	Replaces string `json:"replaces"`
}

func (s *Server) readPush(w http.ResponseWriter, r *http.Request) (pushBody, bool) {
	if s.opts.Push == nil {
		writeJSONError(w, http.StatusNotImplemented, ErrPushUnavailable)
		return pushBody{}, false
	}
	var b pushBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return pushBody{}, false
	}
	return b, true
}

func (s *Server) apiPushKey(w http.ResponseWriter, r *http.Request) {
	if s.opts.Push == nil {
		writeJSONError(w, http.StatusNotImplemented, ErrPushUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": s.opts.Push.PublicKey()})
}

// apiPushSubscribe keeps a subscription, and is called again at every
// opening of the page: see push.Store.Put.
//
// **The origin is where the request arrived**, because that is the page that
// subscribed and where a tap on the notification has to lead. Only an
// encrypted road can subscribe: a browser does not offer push anywhere else,
// and a notification that opened the plain-HTTP address would be opening a
// page no phone outside the house can reach.
func (s *Server) apiPushSubscribe(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readPush(w, r)
	if !ok {
		return
	}
	if !isTLS(r) {
		writeJSONError(w, http.StatusConflict, ErrPushInsecure)
		return
	}
	origin := url.URL{Scheme: "https", Host: r.Host}
	// A renewal keeps the language of what it replaces: the worker that sends
	// it can only say the browser's, and the page may have chosen another.
	lang := b.Lang
	if b.Replaces != "" {
		if was, ok := s.opts.Push.LanguageOf(b.Replaces); ok {
			lang = was
		}
	}
	err := s.opts.Push.Subscribe(push.Subscription{
		Endpoint: b.Endpoint,
		P256dh:   b.Keys.P256dh,
		Auth:     b.Keys.Auth,
		Lang:     lang,
		Origin:   origin.String(),
	})
	switch {
	case errors.Is(err, push.ErrFull):
		writeJSONError(w, http.StatusConflict, ErrPushFull)
	case err != nil:
		s.log.Info("a subscription was refused", "error", err)
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
	default:
		writeOK(w)
	}
}

func (s *Server) apiPushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readPush(w, r)
	if !ok {
		return
	}
	if err := s.opts.Push.Unsubscribe(b.Endpoint); err != nil {
		s.saveFailed(w, err)
		return
	}
	writeOK(w)
}

// apiPushTest sends one notification to the device that asked, and answers
// with the push service's reply **as a code**: the page chooses the words.
func (s *Server) apiPushTest(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readPush(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"result": s.opts.Push.Test(ctx, b.Endpoint)})
}

func (s *Server) apiPushSeen(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readPush(w, r)
	if !ok {
		return
	}
	if len(b.ID) <= 32 {
		s.opts.Push.Seen(b.ID)
	}
	writeOK(w)
}
