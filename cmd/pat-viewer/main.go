// Synthetic viewer: it logs in, negotiates WebRTC the way a browser would and
// checks that video and audio really arrive.
//
// It tests the whole chain (capture, encoding, hub, signalling, SRTP transport)
// automatically, without depending on a browser. The video received is
// reassembled from the RTP packets and analysed with the same parser used in
// production, so what is checked is that the stream is decodable and not merely
// that the bytes arrive.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"patmonitor/internal/media"
)

// pliProbe measures how long passes between asking for a keyframe and the
// keyframe.
//
// It is the proof that the monitor really answers the PLI instead of letting
// the periodic keyframe arrive: with the GOP at 2 seconds, a mean wait around
// one second means nobody listened to the request.
type pliProbe struct {
	mu      sync.Mutex
	waiting bool
	sentAt  time.Time
	waits   []time.Duration
	missed  int
}

// arm records that a PLI has been sent. If the previous one has not been
// answered yet it counts as missed: two requests in flight cannot be told apart.
func (p *pliProbe) arm(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting {
		p.missed++
	}
	p.waiting, p.sentAt = true, now
}

// keyframe records the arrival of an IDR. Those arriving with no request
// pending are the periodic keyframes and say nothing.
func (p *pliProbe) keyframe(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.waiting {
		return
	}
	p.waiting = false
	p.waits = append(p.waits, now.Sub(p.sentAt))
}

func (p *pliProbe) report() (n int, avg, worst time.Duration, missed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	missed = p.missed
	if p.waiting {
		missed++
	}
	var sum time.Duration
	for _, w := range p.waits {
		sum += w
		if w > worst {
			worst = w
		}
	}
	if n = len(p.waits); n > 0 {
		avg = sum / time.Duration(n)
	}
	return n, avg, worst, missed
}

var (
	addr     = flag.String("addr", "http://localhost:8080", "monitor address")
	password = flag.String("password", "", "login password")
	duration = flag.Duration("d", 12*time.Second, "test duration")
	// The monitor absorbs requests that come close together (half a second),
	// because one keyframe repairs every viewer at once: below that threshold
	// the PLIs would look unanswered while everything is in order.
	pliEvery = flag.Duration("pli", 0, "send a PLI at this cadence (>=1s) and measure the keyframe wait")
)

type signalMessage struct {
	Type      string                     `json:"type"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Message   string                     `json:"message,omitempty"`
	Transport string                     `json:"transport,omitempty"`
	// Reason carries the reason for the error as a code.
	//
	// The server no longer sends `Message`, so without this field the report
	// says "error from the server: " and nothing else — which is precisely the
	// information this tool is run for. Here the code is printed bare: a
	// developer reads it, and turning it into a sentence would be one more
	// dictionary to keep aligned for nobody.
	Reason string `json:"reason,omitempty"`
}

func main() {
	flag.Parse()
	if *password == "" {
		fatal("-password is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration+30*time.Second)
	defer cancel()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}

	fmt.Println("== 1. check that everything is closed without authentication ==")
	checkUnauthenticated(client)

	fmt.Println("\n== 2. login ==")
	if err := login(client, *addr, *password); err != nil {
		fatal("login: %v", err)
	}
	fmt.Println("  login succeeded, session cookie obtained")

	fmt.Println("\n== 3. status ==")
	status, err := fetchStatus(client, *addr)
	if err != nil {
		fatal("status: %v", err)
	}
	for _, k := range []string{"encoder", "encoderVendor", "resolution", "camera", "rawAudio", "micHealth", "profileLevelId"} {
		fmt.Printf("  %-15s %v\n", k, status[k])
	}

	plid, _ := status["profileLevelId"].(string)
	if plid == "" {
		fatal("the server has no profile-level-id yet: the stream is not ready")
	}

	fmt.Println("\n== 4. WebRTC negotiation and media reception ==")
	if err := watch(ctx, jar, *addr, plid, *duration); err != nil {
		fatal("%v", err)
	}
}

// newPeerConnection builds the test peer, registering H.264 with the same
// profile-level-id the server announces.
//
// Pion's default media engine registers a single H.264 (42001f): if the server
// is configured with a different profile, the negotiation would fail and produce
// a false alarm. A real browser announces several profiles and is therefore more
// permissive than this client: here it is aligned deliberately, so that the test
// measures the chain and not the client's narrowness.
func newPeerConnection(profileLevelID string) (*webrtc.PeerConnection, error) {
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeH264,
			ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" +
				profileLevelID,
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	// Opus declares 2 channels even though the stream is mono: RFC 7587
	// prescribes it, and the real count travels inside the bitstream. Declaring
	// 1, however better it describes what arrives, finds no match and the
	// negotiation fails with "codec is not supported by remote".
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(engine, ir); err != nil {
		return nil, err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithInterceptorRegistry(ir))
	return api.NewPeerConnection(webrtc.Configuration{})
}

// checkUnauthenticated verifies that the protected endpoints refuse access.
func checkUnauthenticated(client *http.Client) {
	bare := &http.Client{Timeout: 10 * time.Second} // no cookie jar
	for _, path := range []string{"/api/status", "/ws"} {
		res, err := bare.Get(*addr + path)
		if err != nil {
			fmt.Printf("  [??  ] %s: %v\n", path, err)
			continue
		}
		res.Body.Close()
		ok := res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden
		mark := "OK  "
		if !ok {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %s -> %d (expected 401/403)\n", mark, path, res.StatusCode)
		if !ok {
			os.Exit(1)
		}
	}
}

func login(client *http.Client, base, pass string) error {
	body, _ := json.Marshal(map[string]string{"password": pass})
	res, err := client.Post(base+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var e map[string]any
		_ = json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("HTTP %d: %v", res.StatusCode, e["error"])
	}
	return nil
}

func fetchStatus(client *http.Client, base string) (map[string]any, error) {
	res, err := client.Get(base + "/api/status")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var out map[string]any
	return out, json.NewDecoder(res.Body).Decode(&out)
}

// watch runs the negotiation and measures what arrives.
func watch(ctx context.Context, jar *cookiejar.Jar, base, profileLevelID string, d time.Duration) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws", wsScheme, u.Host)

	// The WebSocket has to be authenticated with the same session cookie.
	header := http.Header{}
	var cookies []string
	for _, c := range jar.Cookies(u) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	header.Set("Cookie", strings.Join(cookies, "; "))
	// Origin consistent with the host: the server refuses cross origins.
	header.Set("Origin", base)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return fmt.Errorf("WebSocket open: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(256 << 10)
	fmt.Println("  signalling WebSocket open")

	pc, err := newPeerConnection(profileLevelID)
	if err != nil {
		return fmt.Errorf("PeerConnection: %w", err)
	}
	defer pc.Close()
	fmt.Printf("  test peer aligned on profile-level-id=%s\n", profileLevelID)

	var (
		mu           sync.Mutex
		videoPackets atomic.Int64
		audioPackets atomic.Int64
		videoBytes   atomic.Int64
		audioBytes   atomic.Int64
		// Pictures counted from the **change of RTP timestamp**, which is the
		// only way for one picture to count as one. Counting slice NALs does
		// not work: Quick Sync puts **three** of them per picture, so a real
		// 9.8 fps comes out as "29.8" in the report.
		videoPictures atomic.Int64
		// annexBTruncated says the buffer filled up and the NAL counts cover
		// only the start of the test.
		//
		// **It is here because silent truncation lies.** The buffer stops at
		// 8 MB, which at 1300 kbit/s is half of a two-minute test, and the
		// frames counted were then divided by the **whole** duration: 1804
		// pictures collected in one minute, declared as 15 per second over two
		// minutes, while the camera was delivering 30. A tool that stops
		// measuring and does not say so makes what it measures look broken.
		annexBTruncated bool
		annexB          bytes.Buffer
		transport       string
		connState       string
		// Time declared by the RTP timestamps against real elapsed time.
		videoMediaSecs, videoWallSecs float64
		audioMediaSecs, audioWallSecs float64

		probe     pliProbe
		videoSSRC atomic.Uint32
	)

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		kind := track.Kind().String()
		fmt.Printf("  track received: %s, codec %s\n", kind, track.Codec().MimeType)

		isVideo := track.Kind() == webrtc.RTPCodecTypeVideo
		if isVideo {
			videoSSRC.Store(uint32(track.SSRC()))
		}
		depack := &codecs.H264Packet{}
		clockRate := float64(track.Codec().ClockRate)
		var prevTS uint32
		var firstTS, lastTS uint32
		var firstAt time.Time
		haveFirst := false

		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}

			// Media time against real time: if the RTP timestamps advance less
			// than wall time, the receiver accumulates delay because it is
			// being handed more content than is being declared.
			if !haveFirst {
				firstTS, firstAt, haveFirst = pkt.Timestamp, time.Now(), true
			}
			lastTS = pkt.Timestamp
			mu.Lock()
			mediaSecs := float64(lastTS-firstTS) / clockRate
			wallSecs := time.Since(firstAt).Seconds()
			if isVideo {
				videoMediaSecs, videoWallSecs = mediaSecs, wallSecs
			} else {
				audioMediaSecs, audioWallSecs = mediaSecs, wallSecs
			}
			mu.Unlock()

			if isVideo {
				videoPackets.Add(1)
				videoBytes.Add(int64(len(pkt.Payload)))
				// One picture per timestamp: it is what the decoder does too.
				if videoPictures.Load() == 0 || pkt.Timestamp != prevTS {
					videoPictures.Add(1)
					prevTS = pkt.Timestamp
				}
				// Reassembles the Annex-B from the RTP packets (STAP-A and FU-A included).
				if out, err := depack.Unmarshal(pkt.Payload); err == nil && len(out) > 0 {
					if *pliEvery > 0 {
						now := time.Now()
						media.IterateAnnexB(out, func(n media.NAL) bool {
							if !n.IsKeyframe() {
								return true
							}
							probe.keyframe(now)
							return false
						})
					}
					mu.Lock()
					if annexB.Len() < 8<<20 {
						annexB.Write(out)
					} else {
						annexBTruncated = true
					}
					mu.Unlock()
				}
			} else {
				audioPackets.Add(1)
				audioBytes.Add(int64(len(pkt.Payload)))
			}
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		data, _ := json.Marshal(signalMessage{Type: "candidate", Candidate: &init})
		_ = conn.Write(ctx, websocket.MessageText, data)
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		mu.Lock()
		connState = s.String()
		mu.Unlock()
		fmt.Printf("  connection state: %s\n", s)
	})

	// Signalling loop in the background.
	done := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				done <- nil
				return
			}
			var msg signalMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "offer":
				if msg.SDP == nil {
					continue
				}
				fmt.Println("  SDP offer received")
				if err := pc.SetRemoteDescription(*msg.SDP); err != nil {
					done <- fmt.Errorf("SetRemoteDescription: %w", err)
					return
				}
				answer, err := pc.CreateAnswer(nil)
				if err != nil {
					done <- fmt.Errorf("CreateAnswer: %w", err)
					return
				}
				if err := pc.SetLocalDescription(answer); err != nil {
					done <- fmt.Errorf("SetLocalDescription: %w", err)
					return
				}
				out, _ := json.Marshal(signalMessage{Type: "answer", SDP: pc.LocalDescription()})
				if err := conn.Write(ctx, websocket.MessageText, out); err != nil {
					done <- fmt.Errorf("sending the answer: %w", err)
					return
				}
				fmt.Println("  SDP answer sent")
			case "candidate":
				if msg.Candidate != nil {
					_ = pc.AddICECandidate(*msg.Candidate)
				}
			case "transport":
				mu.Lock()
				transport = msg.Transport
				mu.Unlock()
				fmt.Printf("  ICE path: %s\n", msg.Transport)
			case "error":
				done <- fmt.Errorf("error from the server: %s",
					firstNonEmpty(msg.Reason, msg.Message, "no reason given"))
				return
			}
		}
	}()

	// Keyframe requests, if asked for.
	//
	// It does not wait to really lose packets: the PLI is sent on demand, which
	// is the only way of testing this in the laboratory. The first one goes out
	// after one interval, when the stream has already settled.
	if *pliEvery > 0 {
		stopPLI := make(chan struct{})
		defer close(stopPLI)
		// In the last second nothing more is asked: a request sent as the test
		// ends would go unanswered because time ran out, and would be counted
		// as a fault that is not there.
		lastCall := time.Now().Add(d - time.Second)
		go func() {
			t := time.NewTicker(*pliEvery)
			defer t.Stop()
			for {
				select {
				case <-stopPLI:
					return
				case <-ctx.Done():
					return
				case now := <-t.C:
					if now.After(lastCall) {
						return
					}
					ssrc := videoSSRC.Load()
					if ssrc == 0 {
						continue
					}
					probe.arm(now)
					if err := pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{
						MediaSSRC:  ssrc,
						SenderSSRC: ssrc,
					}}); err != nil {
						fmt.Printf("  sending PLI: %v\n", err)
					}
				}
			}
		}()
	}

	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(d):
	case <-ctx.Done():
	}

	// --- report ---
	mu.Lock()
	stream := annexB.Bytes()
	tr, cs := transport, connState
	vMedia, vWall := videoMediaSecs, videoWallSecs
	aMedia, aWall := audioMediaSecs, audioWallSecs
	mu.Unlock()

	st := media.Analyze(stream)

	fmt.Println()
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("  VERDICT after %s\n", d)
	fmt.Println(strings.Repeat("=", 72))

	pass := true
	check := func(ok bool, format string, a ...any) {
		mark := "OK  "
		if !ok {
			mark, pass = "FAIL", false
		}
		fmt.Printf("  [%s] %s\n", mark, fmt.Sprintf(format, a...))
	}

	check(cs == "connected", "connection state: %s", cs)
	check(videoPackets.Load() > 0, "video: %d RTP packets, %.0f kbit/s",
		videoPackets.Load(), float64(videoBytes.Load())*8/1000/d.Seconds())
	check(audioPackets.Load() > 0, "audio: %d RTP packets, %.0f kbit/s",
		audioPackets.Load(), float64(audioBytes.Load())*8/1000/d.Seconds())
	check(st.SPS > 0 && st.PPS > 0, "parameter sets reassembled: SPS=%d PPS=%d, profile-level-id=%s",
		st.SPS, st.PPS, st.ProfileLevelID)
	check(st.IDR > 0, "keyframes reassembled: %d", st.IDR)
	// **The cadence is counted on the timestamps, not on the slice NALs.** One
	// picture can be split across several slices — Quick Sync makes three — and
	// counting those inflates the cadence by as much: a real 9.8 fps was
	// reported as 29.8. The RTP timestamp instead changes once per picture,
	// which is also how the decoder separates them.
	check(videoPictures.Load() > 0, "pictures received: %d (%.1f fps)",
		videoPictures.Load(), float64(videoPictures.Load())/d.Seconds())
	// The NALs are counted on the buffer, which has a ceiling: when it fills
	// the count covers only the start, and that has to be said instead of being
	// divided by the whole duration — that way a real 30 fps came out as "15".
	if annexBTruncated {
		fmt.Printf("  [    ] slices reassembled: %d, but the buffer filled up: "+
			"the count covers only the start of the test\n", st.Frames())
	} else {
		fmt.Printf("  [    ] slices reassembled: %d (%.1f per picture)\n",
			st.Frames(), float64(st.Frames())/math.Max(1, float64(videoPictures.Load())))
	}

	// If the time declared by the RTP timestamps advances less than real time,
	// the receiver is getting more content than is being declared and the delay
	// grows without limit. A tolerance of 1% absorbs the ordinary fluctuations
	// of the webcam's frame rate.
	const driftTolerance = 0.01
	if vWall > 1 {
		drift := (vMedia - vWall) / vWall
		check(math.Abs(drift) < driftTolerance,
			"video clock drift: %.2fs declared over %.2fs real (%+.2f%%)",
			vMedia, vWall, drift*100)
	}
	if aWall > 1 {
		drift := (aMedia - aWall) / aWall
		check(math.Abs(drift) < driftTolerance,
			"audio clock drift: %.2fs declared over %.2fs real (%+.2f%%)",
			aMedia, aWall, drift*100)
	}
	if tr != "" {
		check(!strings.Contains(tr, "relay"), "media path: %s", tr)
	}

	// With the GOP at 2 seconds, a keyframe left to arrive on its own makes one
	// wait a second on average. Half a second is therefore the threshold that
	// tells a request that was listened to from a coincidence.
	if *pliEvery > 0 {
		n, avg, worst, missed := probe.report()
		check(n > 0 && missed == 0 && worst < 500*time.Millisecond,
			"keyframe on demand: %d PLIs served (mean %v, worst %v), %d unanswered",
			n, avg.Round(time.Millisecond), worst.Round(time.Millisecond), missed)
	}

	fmt.Println(strings.Repeat("-", 72))
	if !pass {
		fmt.Println("  The WebRTC chain is NOT working correctly.")
		return errors.New("check failed")
	}
	fmt.Println("  Whole chain working: a browser would see and hear.")
	return nil
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
