package android

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// usageTimeout bounds one sample, which is taken every few seconds.
const usageTimeout = 5 * time.Second

// AppUsage reads the CPU time and resident memory of an app's main process from /proc, in one
// adb call: its pid, the user and system clock ticks of /proc/<pid>/stat, VmRSS, and the ticks
// per second.
func (c *Client) AppUsage(ctx context.Context, d device.Device, id string) (device.Usage, error) {
	adb, err := c.appTool(d, id)
	if err != nil {
		return device.Usage{}, err
	}
	script := fmt.Sprintf(`pid=$(pidof -s %s) && echo $pid && cut -d" " -f14,15 /proc/$pid/stat && grep VmRSS /proc/$pid/status && getconf CLK_TCK`, id)
	argv := []string{adb, "-s", d.TargetID(), "shell", script}
	res := c.run(ctx, argv, usageTimeout)
	// No output at all, or a pid and nothing more, means the app is not running or stopped
	// meanwhile. The exit code does not tell: an adb without the shell protocol always returns 0.
	out := strings.TrimSpace(strings.ReplaceAll(res.Stdout, "\r", ""))
	_, pidErr := strconv.Atoi(out)
	if res.Err == nil && !res.Missing && ((out == "" && strings.TrimSpace(res.Stderr) == "") || pidErr == nil) {
		return device.Usage{}, apperr.New(apperr.InvalidState, "%s is not running on %s", id, d.Name).WithHint("Launch it first.")
	}
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return device.Usage{}, err
	}
	usage, ok := ParseUsage(res.Stdout)
	if !ok {
		return device.Usage{}, &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the use of " + id, Argv: argv, Stderr: res.Stdout}
	}
	return usage, nil
}

// ParseUsage reads what AppUsage's script prints: the pid, the user and system ticks, the VmRSS
// line in kB, and the ticks per second.
func ParseUsage(out string) (device.Usage, bool) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")), "\n")
	if len(lines) != 4 {
		return device.Usage{}, false
	}
	pid, err1 := strconv.Atoi(strings.TrimSpace(lines[0]))
	ticks := strings.Fields(lines[1])
	rss := strings.Fields(strings.TrimPrefix(lines[2], "VmRSS:"))
	perSecond, err2 := strconv.ParseInt(strings.TrimSpace(lines[3]), 10, 64)
	if err1 != nil || err2 != nil || perSecond <= 0 || len(ticks) != 2 || len(rss) != 2 || rss[1] != "kB" {
		return device.Usage{}, false
	}
	user, err1 := strconv.ParseInt(ticks[0], 10, 64)
	system, err2 := strconv.ParseInt(ticks[1], 10, 64)
	kb, err3 := strconv.ParseInt(rss[0], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return device.Usage{}, false
	}
	cpu := time.Duration(user+system) * time.Second / time.Duration(perSecond)
	return device.Usage{PID: pid, CPU: cpu, Memory: kb * 1024}, true
}
