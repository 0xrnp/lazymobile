package cli

import (
	"context"
	"flag"
	"fmt"
	"sync"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// manyAtOnce is how many devices are worked on together. Booting and installing are heavy, so a
// long list is done in turns.
const manyAtOnce = 3

// Outcomes of one device in a command that works on several.
const (
	outcomeOK      = "ok"
	outcomeSkipped = "skipped"
	outcomeFailed  = "failed"
)

type outcome struct {
	Device  device.Device `json:"device"`
	Status  string        `json:"status"`
	Message string        `json:"message,omitempty"`
	Error   *apperr.Error `json:"error,omitempty"`
}

// several is what `boot` and `install` share when they take more than one device.
type several struct {
	all bool
	set string
}

func (s *several) flags(fs *flag.FlagSet) {
	fs.BoolVar(&s.all, "all", false, "")
	fs.StringVar(&s.set, "set", "", "")
}

func (s several) wanted() bool { return s.all || s.set != "" }

// devices are the ones named by --set, or by the patterns given to --all.
func (a *App) setDevices(ctx context.Context, s several, patterns []string) ([]device.Device, error) {
	if s.all && s.set != "" {
		return nil, usageError{"use --all or --set, not both"}
	}
	if s.set != "" {
		if len(patterns) > 0 {
			return nil, usageError{"--set takes no device names: they come from the set"}
		}
		if a.DeviceSets == nil {
			return nil, apperr.New(apperr.NotFound, "No device set is named %q", s.set)
		}
		sets, err := a.DeviceSets()
		if err != nil {
			return nil, err
		}
		if patterns, err = sets.Patterns(s.set); err != nil {
			return nil, err
		}
	}
	if len(patterns) == 0 {
		return nil, usageError{`--all needs a device name or pattern, such as "iPhone*"`}
	}
	return a.Manager.Select(ctx, patterns)
}

// eachDevice runs work for every device, a few at a time, and returns the outcomes in order.
func eachDevice(ctx context.Context, list []device.Device, work func(device.Device) outcome) []outcome {
	out := make([]outcome, len(list))
	slots := make(chan struct{}, manyAtOnce)
	var wg sync.WaitGroup
	for i, d := range list {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			if ctx.Err() != nil {
				out[i] = outcome{Device: d, Status: outcomeFailed, Message: "interrupted"}
				return
			}
			out[i] = work(d)
		}()
	}
	wg.Wait()
	return out
}

func failed(d device.Device, err error) outcome {
	e := asError(err)
	return outcome{Device: d, Status: outcomeFailed, Message: e.Message, Error: e}
}

// report prints the outcomes, and returns an error when any failed so the exit code says so.
func (a *App) report(action string, asJSON bool, list []outcome) error {
	bad := 0
	for _, o := range list {
		if o.Status == outcomeFailed {
			bad++
		}
	}
	if asJSON {
		if err := a.writeJSON(map[string]any{"ok": bad == 0, "action": action, "results": list}); err != nil {
			return err
		}
	} else {
		for _, o := range list {
			line := fmt.Sprintf("%-8s %s", o.Status, o.Device.Name)
			if o.Message != "" {
				line += ": " + o.Message
			}
			fmt.Fprintln(a.Stdout, line)
		}
	}
	if bad > 0 {
		return reportedError{fmt.Errorf("%d of %d devices failed", bad, len(list))}
	}
	return nil
}

func (a *App) bootSeveral(ctx context.Context, s several, patterns []string, opts mobile.BootOptions, noWindow, asJSON bool) error {
	list, err := a.setDevices(ctx, s, patterns)
	if err != nil {
		return err
	}
	a.Manager.OpenWindow = !noWindow
	results := eachDevice(ctx, list, func(d device.Device) outcome {
		if why := mobile.BootSkip(d); why != "" {
			return outcome{Device: d, Status: outcomeSkipped, Message: why}
		}
		if err := a.Manager.Boot(ctx, d, opts); err != nil {
			return failed(d, err)
		}
		if opts.Wait {
			return outcome{Device: d, Status: outcomeOK, Message: "is ready"}
		}
		return outcome{Device: d, Status: outcomeOK, Message: "is starting"}
	})
	return a.report("boot_all", asJSON, results)
}

func (a *App) installSeveral(ctx context.Context, s several, path string, phones, asJSON bool) error {
	var list []device.Device
	var err error
	if s.all {
		// Every running device that can take this build. Others are not mentioned: they are not
		// what was asked for.
		all, _ := a.Manager.List(ctx)
		for _, d := range all {
			if mobile.InstallSkip(d, path, phones) == "" {
				list = append(list, d)
			}
		}
		if len(list) == 0 {
			return apperr.New(apperr.NotFound, "No running device can take %s", path).
				WithHint("Boot a device, or add --phones to include real phones.")
		}
	} else if list, err = a.setDevices(ctx, s, nil); err != nil {
		return err
	}
	results := eachDevice(ctx, list, func(d device.Device) outcome {
		if why := mobile.InstallSkip(d, path, phones); why != "" {
			return outcome{Device: d, Status: outcomeSkipped, Message: why}
		}
		if err := a.Manager.Install(ctx, d, path); err != nil {
			return failed(d, err)
		}
		return outcome{Device: d, Status: outcomeOK, Message: "installed"}
	})
	return a.report("install_all", asJSON, results)
}
