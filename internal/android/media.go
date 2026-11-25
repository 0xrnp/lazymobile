package android

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// imageTypes and videoTypes are what an emulator's gallery shows, by file extension.
var (
	imageTypes = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic"}
	videoTypes = []string{".mp4", ".3gp", ".webm", ".mkv"}
)

// MediaTypes lists the extensions AddMedia takes.
func MediaTypes() []string { return append(append([]string{}, imageTypes...), videoTypes...) }

func hasType(name string, types []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, t := range types {
		if ext == t {
			return true
		}
	}
	return false
}

// unsafeNameChars is what is replaced in a file's name on the device, since the name goes through
// a shell there.
var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// AddMedia puts a photo or video from this computer into a running emulator's gallery: it is copied
// into Pictures or Movies, under its own name unless that is taken, and then announced to the
// media scanner so that Photos shows it. It returns the path on the device. Only an emulator is
// filled, since a phone's gallery is the person's own.
func (c *Client) AddMedia(ctx context.Context, d device.Device, local string) (string, error) {
	adb, err := c.emulator(d, "add a photo or video")
	if err != nil {
		return "", err
	}
	folder := "/sdcard/Pictures/"
	switch {
	case hasType(local, videoTypes):
		folder = "/sdcard/Movies/"
	case !hasType(local, imageTypes):
		return "", apperr.New(apperr.InvalidState, "An emulator's gallery takes %s, not %s", strings.Join(MediaTypes(), " "), filepath.Ext(local))
	}
	stem := unsafeNameChars.ReplaceAllString(strings.TrimSuffix(filepath.Base(local), filepath.Ext(local)), "_")
	ext := strings.ToLower(filepath.Ext(local))
	target := folder + stem + ext
	// Never replace a picture that is already there.
	for n := 2; n < 100; n++ {
		argv := []string{adb, "-s", d.TargetID(), "shell", "test", "-e", shellQuote(target), "&&", "echo", "taken"}
		res := c.run(ctx, argv, actionTimeout)
		// test -e exits 1 when the file is not there, which is what is wanted.
		if res.Missing || res.TimedOut || res.Err != nil || res.Code > 1 {
			return "", apperr.FromResult(res, argv, installHint["adb"])
		}
		if !strings.Contains(res.Stdout, "taken") {
			break
		}
		target = fmt.Sprintf("%s%s-%d%s", folder, stem, n, ext)
	}
	push := []string{adb, "-s", d.TargetID(), "push", local, target}
	if err := apperr.FromResult(c.run(ctx, push, 2*actionTimeout), push, installHint["adb"]); err != nil {
		return "", err
	}
	scan := []string{adb, "-s", d.TargetID(), "shell", "am", "broadcast", "-a", "android.intent.action.MEDIA_SCANNER_SCAN_FILE", "-d", shellQuote("file://" + target)}
	if err := apperr.FromResult(c.run(ctx, scan, actionTimeout), scan, installHint["adb"]); err != nil {
		return "", err
	}
	return target, nil
}
