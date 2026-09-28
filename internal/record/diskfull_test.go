package record

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// **The kept clips have no ceiling, and the disk's free space is the floor
// under every clip.** Below it nothing is written, kept or not; the refusal and
// the recovery are each written once rather than once per clip; and a disk that
// cannot be asked is not a full one, since refusing then would switch the
// recordings off for a failure that has nothing to do with space.
//
// **The defect was put back and this test fails with it**: without the check
// in Save the clip is written on the full disk.
func TestNoClipIsWrittenOnANearlyFullDisk(t *testing.T) {
	var free uint64 = MinFreeBytes - 1
	var askErr error
	var logged bytes.Buffer
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{
		FreeSpace: func(string) (uint64, error) { return free, askErr },
		Log:       slog.New(slog.NewTextHandler(&logged, nil)),
	})

	ring := newRing(nil)
	feedGOP(t, ring, sps720p, t0, 20, step)
	clip := Clip{Snapshot: ring.Snapshot(), Code: "motion", At: t0, Keep: true}

	for range 3 {
		if err := s.Save(clip); !errors.Is(err, ErrDiskFull) {
			t.Fatalf("on a full disk Save answered %v", err)
		}
	}
	if names := namesIn(t, dir); len(names) != 0 {
		t.Errorf("a kept clip was written on a full disk: %v", names)
	}
	if !errors.Is(s.Room(), ErrDiskFull) {
		t.Error("Room says there is room on a full disk")
	}
	if n := strings.Count(logged.String(), "clips are not saved"); n != 1 {
		t.Errorf("three refused clips wrote the refusal %d times, wanted once", n)
	}

	free = MinFreeBytes
	if err := s.Save(clip); err != nil {
		t.Fatalf("with room again Save answered %v", err)
	}
	if n := strings.Count(logged.String(), "clips are saved again"); n != 1 {
		t.Errorf("the recovery was written %d times, wanted once", n)
	}

	free, askErr = 0, errors.New("the disk would not say")
	clip.At = clip.At.Add(1e9)
	if err := s.Save(clip); err != nil {
		t.Errorf("a disk that could not be asked refused the clip: %v", err)
	}
	if s.Room() != nil {
		t.Error("a disk that could not be asked was declared full")
	}
}

// **A disk that cannot be asked after a full one is not declared to have room.**
// The unanswered branch shared the recovery line, which then said there was
// room with free_mb=0.
//
// **The defect was put back and this test fails with it**: with the two
// branches shared again, the recovery line is written on no answer.
func TestNoAnswerAfterAFullDiskIsNotRoom(t *testing.T) {
	var free uint64 = MinFreeBytes - 1
	var askErr error
	var logged bytes.Buffer
	s := NewStore(t.TempDir(), StoreConfig{
		FreeSpace: func(string) (uint64, error) { return free, askErr },
		Log:       slog.New(slog.NewTextHandler(&logged, nil)),
	})
	ring := newRing(nil)
	feedGOP(t, ring, sps720p, t0, 20, step)
	clip := Clip{Snapshot: ring.Snapshot(), Code: "motion", At: t0}
	if err := s.Save(clip); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("on a full disk Save answered %v", err)
	}

	askErr = errors.New("the share is gone")
	if err := s.Save(clip); err != nil {
		t.Errorf("a disk that could not be asked refused the clip: %v", err)
	}
	if strings.Contains(logged.String(), "clips are saved again") {
		t.Errorf("no answer about the disk was written as room on it:\n%s", logged.String())
	}
}
