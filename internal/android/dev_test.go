package android

import (
	"context"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestDevHelpersSendTheRightAdbCommands(t *testing.T) {
	ctx := context.Background()
	fake := runtest.Table(map[string]run.Result{"adb": runtest.Out("")})
	c := New(fake, sdk(t, "adb"))
	if err := c.ReversePort(ctx, runningEmulator, 8081); err != nil {
		t.Fatal(err)
	}
	if err := c.OpenDevMenu(ctx, runningEmulator); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(ctx, runningEmulator); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"adb -s emulator-5554 reverse tcp:8081 tcp:8081",
		"adb -s emulator-5554 shell input keyevent 82",
		"adb -s emulator-5554 shell input keyevent 46 46",
	} {
		if !fake.Called(want) {
			t.Errorf("did not run %q; ran %v", want, fake.Calls())
		}
	}
}

func TestDevHelpersNeedARunningAndroidDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	c := New(fake, sdk(t, "adb"))
	stopped := runningEmulator
	stopped.State = "shutdown"
	if err := c.Reload(context.Background(), stopped); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("error = %v, want invalid_state", err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("adb ran for a stopped device: %v", fake.Calls())
	}
}

func TestDevHelperReportsAFailingAdb(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"reverse": runtest.Fail(1, "error: device offline")})
	err := New(fake, sdk(t, "adb")).ReversePort(context.Background(), runningEmulator, 8081)
	if apperr.CodeOf(err) != apperr.CommandFailed {
		t.Errorf("error = %v, want command_failed", err)
	}
}
