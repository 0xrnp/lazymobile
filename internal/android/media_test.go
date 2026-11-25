package android

import (
	"context"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestAddMediaCopiesIntoPicturesOrMoviesAndAsksTheScannerToLookAtIt(t *testing.T) {
	ctx := context.Background()
	for local, want := range map[string]string{
		"/home/me/My Photo (1).PNG": "/sdcard/Pictures/My_Photo__1_.png",
		"/home/me/demo.mp4":         "/sdcard/Movies/demo.mp4",
	} {
		fake := runtest.Table(map[string]run.Result{"shell test -e": runtest.Fail(1, ""), "push": runtest.Out("1 file pushed\n"), "am broadcast": runtest.Out("Broadcast completed: result=0\n")})
		got, err := New(fake, sdk(t, "adb")).AddMedia(ctx, runningEmulator, local)
		if err != nil || got != want {
			t.Fatalf("%s: %q, %v", local, got, err)
		}
		if !fake.Called("-s emulator-5554 push "+local+" "+want) || !fake.Called("MEDIA_SCANNER_SCAN_FILE -d 'file://"+want+"'") {
			t.Errorf("%s: calls %q", local, fake.Calls())
		}
	}
}

func TestAddMediaNeverReplacesAPictureAndRefusesWhatItCannotShow(t *testing.T) {
	ctx := context.Background()
	taken := 0
	fake := &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		if strings.Contains(command, "shell test -e") {
			// The first two names are taken.
			if taken++; taken <= 2 {
				return runtest.Out("taken\n")
			}
			return runtest.Fail(1, "")
		}
		return runtest.Out("ok\n")
	}}
	got, err := New(fake, sdk(t, "adb")).AddMedia(ctx, runningEmulator, "/tmp/shot.png")
	if err != nil || got != "/sdcard/Pictures/shot-3.png" {
		t.Errorf("%q, %v", got, err)
	}
	phone := device.Device{ID: "R5", Name: "Pixel", Platform: device.Android, Kind: device.Physical, State: device.Booted, Serial: "R5"}
	for name, tc := range map[string]struct {
		d     device.Device
		local string
		code  apperr.Code
	}{"a phone": {phone, "/tmp/a.png", apperr.Unsupported}, "a document": {runningEmulator, "/tmp/a.pdf", apperr.InvalidState}} {
		none := runtest.Table(map[string]run.Result{})
		if _, err := New(none, sdk(t, "adb")).AddMedia(ctx, tc.d, tc.local); apperr.CodeOf(err) != tc.code || len(none.Calls()) != 0 {
			t.Errorf("%s: %v, calls %q", name, err, none.Calls())
		}
	}
}
