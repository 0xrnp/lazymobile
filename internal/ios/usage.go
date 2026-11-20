package ios

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// usageTimeout bounds one sample, which is taken every few seconds.
const usageTimeout = 5 * time.Second

// AppUsage reads the CPU time and resident memory of an app on a booted simulator. Its process
// runs on the Mac, so ps reads it. The pid of the sample before is tried first, since finding it
// asks the simulator's launchd, which takes most of a second.
func (c *Client) AppUsage(ctx context.Context, d device.Device, bundleID string, pid int) (device.Usage, error) {
	if err := onlySimulator(d, "read an app's CPU and memory"); err != nil {
		return device.Usage{}, err
	}
	if pid > 0 {
		if usage, ok := c.processUsage(ctx, pid); ok {
			return usage, nil
		}
	}
	argv := []string{"xcrun", "simctl", "spawn", d.ID, "launchctl", "list"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: usageTimeout})
	if err := actionError(res, argv, action{verb: "read an app's CPU and memory", hint: "Boot it first."}); err != nil {
		return device.Usage{}, err
	}
	pid = AppPID(res.Stdout, bundleID)
	if pid == 0 {
		return device.Usage{}, apperr.New(apperr.InvalidState, "%s is not running on %s", bundleID, d.Name).WithHint("Launch it first.")
	}
	if usage, ok := c.processUsage(ctx, pid); ok {
		return usage, nil
	}
	return device.Usage{}, apperr.New(apperr.InvalidState, "%s stopped on %s", bundleID, d.Name).WithHint("Launch it again.")
}

func (c *Client) processUsage(ctx context.Context, pid int) (device.Usage, bool) {
	res := c.Runner.Run(ctx, []string{"ps", "-o", "time=,rss=", "-p", strconv.Itoa(pid)}, run.Options{Timeout: usageTimeout})
	if res.Err != nil || res.Code != 0 {
		return device.Usage{}, false
	}
	usage, ok := ParsePSUsage(res.Stdout)
	usage.PID = pid
	return usage, ok
}

// appJob is an app's line in a simulator's `launchctl list`: its pid, its last exit status, and
// its label, UIKitApplication:<bundle id>[...].
var appJob = regexp.MustCompile(`(?m)^(\d+)\s+\S+\s+UIKitApplication:([^\[\s]+)\[`)

// AppPID is the pid of bundleID's running app in a simulator's `launchctl list`, or 0.
func AppPID(list, bundleID string) int {
	for _, m := range appJob.FindAllStringSubmatch(list, -1) {
		if m[2] == bundleID {
			pid, _ := strconv.Atoi(m[1])
			return pid
		}
	}
	return 0
}

// ParsePSUsage reads `ps -o time=,rss=`: the CPU time as [[dd-]hh:]mm:ss.cc, and the resident
// memory in kilobytes.
func ParsePSUsage(out string) (device.Usage, bool) {
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return device.Usage{}, false
	}
	kb, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return device.Usage{}, false
	}
	clock := fields[0]
	var days int64
	if d, rest, ok := strings.Cut(clock, "-"); ok {
		if days, err = strconv.ParseInt(d, 10, 64); err != nil {
			return device.Usage{}, false
		}
		clock = rest
	}
	parts := strings.Split(clock, ":")
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || len(parts) > 3 {
		return device.Usage{}, false
	}
	total := float64(days*86400) + seconds
	for i, unit := range []float64{60, 3600} {
		if j := len(parts) - 2 - i; j >= 0 {
			n, err := strconv.ParseInt(parts[j], 10, 64)
			if err != nil {
				return device.Usage{}, false
			}
			total += float64(n) * unit
		}
	}
	return device.Usage{CPU: time.Duration(total * float64(time.Second)), Memory: kb * 1024}, true
}
