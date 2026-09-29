package pipeline

import "time"

// maxCaptureAge is the oldest a frame may claim to be before its own
// timestamp is disbelieved.
//
// Measured on the AMD machine, a frame leaves the encoder 41-121 ms after the
// instant the camera stamped on it. A second is ten times that, so it never
// bites on a camera whose timestamps are on the system clock, and it catches
// one whose timestamps are not — counted from its own start, say — where the
// difference is the machine's uptime.
const maxCaptureAge = time.Second

// capturedAt says when a frame was captured, on the wall clock the recorder
// dates everything with.
//
// **A frame reaches the sinks late, and not as late as the sound does.**
// Dated on arrival, the picture in a clip is placed where the encoder let it
// go rather than where the camera saw it: 41-121 ms after, measured, while a
// packet of audio is 10 ms old when it is encoded. The two tracks sit on one
// clock in the file, so the difference was heard as the sound arriving before
// the picture. The camera stamps each frame with the instant it was captured,
// and the transform carries that stamp through to the coded frame, so the age
// of the frame is known: clock minus stamp.
//
// pts is the frame's timestamp and clock is the same clock read now, both in
// the units mf.SystemTime reads. It answers now, and false, when the frame has
// no timestamp or one that cannot be the capture instant: in the future, or
// older than maxCaptureAge. Arrival is the fallback because it is what every
// clip was dated with before, and a frame placed late is better than one
// placed nowhere.
func capturedAt(pts time.Duration, ok bool, clock time.Duration, now time.Time) (time.Time, bool) {
	if !ok {
		return now, false
	}
	age := clock - pts
	if age < 0 || age > maxCaptureAge {
		return now, false
	}
	return now.Add(-age), true
}

// packetStart says when the first sample of the next Opus packet was captured.
//
// queued is how many samples are waiting to be encoded, the packet's own
// included, and the last of them arrived with the block handed over at
// arrived: so the packet began that many samples earlier. It is the audio's
// half of capturedAt, and it is needed for the same reason — a packet is
// encoded when the block that completes it arrives, which can be up to a block
// after its first sample was heard.
func packetStart(arrived time.Time, queued, sampleRate int) time.Time {
	return arrived.Add(-time.Duration(queued) * time.Second / time.Duration(sampleRate))
}
