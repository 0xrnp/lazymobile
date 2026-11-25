package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// AppJank reads how many of an app's frames were janky from `dumpsys gfxinfo`. With reset, the
// numbers are cleared afterwards, so the next read covers only what happens from now on. gfxinfo
// prints the counters first and clears them after, so the result is what the app had until now.
func (c *Client) AppJank(ctx context.Context, d device.Device, id string, reset bool) (device.Jank, error) {
	adb, err := c.appTool(d, id)
	if err != nil {
		return device.Jank{}, err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "dumpsys", "gfxinfo", id}
	if reset {
		argv = append(argv, "reset")
	}
	res := c.run(ctx, argv, usageTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return device.Jank{}, err
	}
	if strings.Contains(res.Stdout, "No process found") {
		return device.Jank{}, apperr.New(apperr.InvalidState, "%s is not running on %s", id, d.Name).WithHint("Launch it first.")
	}
	jank, ok := ParseJank(res.Stdout)
	if !ok {
		return device.Jank{}, &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the frame stats of " + id, Argv: argv, Stderr: res.Stdout}
	}
	return jank, nil
}

var (
	totalFramesLine = regexp.MustCompile(`(?m)^Total frames rendered: (\d+)\s*$`)
	jankyFramesLine = regexp.MustCompile(`(?m)^Janky frames: (\d+) \(([\d.]+)%\)\s*$`)
	percentileLine  = regexp.MustCompile(`(?m)^(50|90|95|99)th percentile: (\d+)ms\s*$`)
)

// ParseJank reads the frame counters of `dumpsys gfxinfo <package>`. The legacy janky count and the
// GPU percentiles are ignored. An app that has drawn nothing since a reset still lists
// percentiles, but they are meaningless, so they are left at 0.
func ParseJank(out string) (device.Jank, bool) {
	total := totalFramesLine.FindStringSubmatch(out)
	janky := jankyFramesLine.FindStringSubmatch(out)
	if total == nil || janky == nil {
		return device.Jank{}, false
	}
	frames, err1 := strconv.Atoi(total[1])
	count, err2 := strconv.Atoi(janky[1])
	percent, err3 := strconv.ParseFloat(janky[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return device.Jank{}, false
	}
	jank := device.Jank{Frames: frames, Janky: count, Percent: percent}
	if frames == 0 {
		return jank, true
	}
	for _, m := range percentileLine.FindAllStringSubmatch(out, -1) {
		ms, _ := strconv.Atoi(m[2])
		switch m[1] {
		case "50":
			jank.P50 = ms
		case "90":
			jank.P90 = ms
		case "95":
			jank.P95 = ms
		case "99":
			jank.P99 = ms
		}
	}
	return jank, true
}
