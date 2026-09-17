package ced

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"patmonitor/internal/guard"
)

// Result is the outcome of one classification.
type Result struct {
	// At is when the classification **finished**. The sound it describes is
	// that of the seconds before, which is why Seconds is here too.
	At time.Time
	// Seconds is how much audio was really in the window. Below a full window
	// the model still answers, but with less context: whoever reads a score has
	// to know how much it was taken over.
	Seconds float64
	// Took is what it cost.
	Took time.Duration
	// Scores are the 527 probabilities, **independent**: they do not sum to one.
	Scores []float32
	// Err is what stopped it answering. With Err non-nil the other fields say
	// nothing.
	Err error
}

// ErrWrongRate says the stream does not arrive at the model's rate.
//
// **In the monitor it should never happen**: the analysis stream comes out at
// 16 kHz for any microphone, averaging where the ratio is a whole number and
// resampling where it is not.
//
// It stays because this package is not the monitor and cannot take its word for
// it. **A stream at the wrong rate is not classified**: the mel bands are
// anchored to 16 kHz, and on a 24 kHz stream the model would answer as if every
// sound were a third higher — plausibly and at random. Whoever delivers has to
// **declare** it, though, otherwise the recognition vanishes and from outside
// it looks like a quiet room: that is done by cmd/pat-monitor/recognise.go,
// which is where there is a log.
var ErrWrongRate = errors.New("ced: the analysis stream is not at the model's rate")

// Stream keeps the last window of audio and has it classified when somebody
// asks.
//
// **Whoever delivers the audio never waits.** FeedS16 is called from the
// capture goroutine, fifty times a second, and one classification costs half a
// second: the work sits on a goroutine of its own, and between the two there is
// a copy. Blocking that callback would mean losing audio packets to look for a
// cry, that is, breaking the very thing the program exists to protect.
//
// **And the cadence is not dictated by whoever wants it.** An episode lasting
// three minutes would ask for a classification every time the gate lights up
// again; here one is done every `interval`, and the requests in between are
// dropped. Without that, half a second of work every half second would be a
// whole core for as long as a cry lasts.
type Stream struct {
	m        *Model
	interval time.Duration

	mu     sync.Mutex
	rate   int
	ring   []float32
	head   int
	filled int

	ask     chan struct{}
	stop    chan struct{}
	done    chan struct{}
	closing sync.Once
	last    atomic.Pointer[Result]
}

// NewStream opens the recogniser. It has to be closed.
//
// `interval` is the minimum wait between two classifications, and zero means
// none. Five seconds cost ~10% of a core while an episode is in progress, and
// give two or three verdicts on a cry that lasts.
//
// **It is a parameter and not a field written afterwards**, because what reads
// it is the goroutine that starts here: a public field invites being set right
// after construction, which is a race between two goroutines. On this machine
// Go's detector cannot be run — it wants cgo, and there is no C compiler — so a
// race like that would be found by nobody.
func NewStream(m *Model, interval time.Duration) *Stream {
	s := &Stream{
		m:        m,
		interval: interval,
		ring:     make([]float32, m.WindowSamples()),
		ask:      make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	guard.Go(nil, "the sound recogniser", func() { s.serve() })
	return s
}

// FeedS16 appends mono s16le PCM samples, declaring what rate they are at.
//
// **The rate travels with the samples** instead of being set separately: the
// microphone can be changed while the monitor runs, and then the analysis
// stream changes too. A caller who had to remember to announce it would forget
// at the first new call site — it is the same reason the motion frame carries
// the size it came from.
func (s *Stream) FeedS16(pcm []byte, rate int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rate != s.rate {
		// The past was at another rate: it does not get mixed in.
		s.rate, s.head, s.filled = rate, 0, 0
	}
	if rate != s.m.rate {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		s.ring[s.head] = float32(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768
		s.head++
		if s.head == len(s.ring) {
			s.head = 0
		}
		if s.filled < len(s.ring) {
			s.filled++
		}
	}
}

// Ready says whether there is enough audio, at the right rate, for a question
// to have an answer.
func (s *Stream) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rate == s.m.rate && s.filled > 0
}

// Ask asks for a classification of the current window, and **does not wait**.
// It returns false if the request was not taken: either there is no audio, or
// there is one queued already. A refusal is not a fault — it means an answer is
// already on its way.
func (s *Stream) Ask() bool {
	if !s.Ready() {
		return false
	}
	select {
	case s.ask <- struct{}{}:
		return true
	default:
		return false
	}
}

// Last is the latest outcome, or nil if there has not been one yet.
func (s *Stream) Last() *Result { return s.last.Load() }

// Close stops the goroutine and waits for it to finish. It can be called more
// than once, and from different goroutines.
//
// **The guard is a sync.Once and not a select on the channel.** Looking at
// whether the channel is already closed and then closing it is a race between
// the two lines: two callers both pass the check and the second close panics.
// It does not happen while only one caller closes — which is today's case — and
// that is exactly the kind of thing that stops being true with nobody noticing.
func (s *Stream) Close() error {
	s.closing.Do(func() { close(s.stop) })
	<-s.done
	return nil
}

func (s *Stream) serve() {
	defer close(s.done)
	var lastRun time.Time
	for {
		select {
		case <-s.stop:
			return
		case <-s.ask:
			// The wait applies here and not to whoever asks: that way the cap
			// holds for every road a request can come from.
			if wait := s.interval - time.Since(lastRun); wait > 0 && !lastRun.IsZero() {
				select {
				case <-s.stop:
					return
				case <-time.After(wait):
				}
			}
			w, secs, err := s.snapshot()
			t0 := time.Now()
			var scores []float32
			if err == nil {
				scores, err = s.m.Scores(w)
			}
			lastRun = time.Now()
			s.last.Store(&Result{
				At: lastRun, Seconds: secs, Took: lastRun.Sub(t0),
				Scores: scores, Err: err,
			})
		}
	}
}

// snapshot copies the window out of the ring.
//
// **It copies, and that is not waste**: the classification lasts half a second
// and the ring keeps being rewritten for all of it. Working on the ring would
// give a window that halfway through the computation holds audio that arrived
// later, that is, a spectrogram of two different moments stitched together —
// which produces no error and moves the verdict.
func (s *Stream) snapshot() ([]float32, float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rate != s.m.rate {
		return nil, 0, ErrWrongRate
	}
	n := s.filled
	out := make([]float32, n)
	// The ring is circular: the oldest sample sits at head-filled.
	start := (s.head - n + len(s.ring)) % len(s.ring)
	copied := copy(out, s.ring[start:])
	copy(out[copied:], s.ring[:n-copied])
	return out, float64(n) / float64(s.rate), nil
}
