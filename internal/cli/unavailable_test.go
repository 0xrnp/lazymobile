package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

const (
	usableSim = `{"udid": "00000000-0000-4000-8000-0000000000A1", "name": "iPhone 16", "state": "Shutdown", "isAvailable": true, "dataPathSize": 1854255104}`
	orphanOne = `{"udid": "00000000-0000-4000-8000-0000000000D1", "name": "iPhone 18 Pro", "state": "Shutdown", "isAvailable": false, "dataPathSize": 1854255104, "availabilityError": "runtime profile not found"}`
	orphanTwo = `{"udid": "00000000-0000-4000-8000-0000000000D2", "name": "iPhone 18 Pro Max", "state": "Shutdown", "isAvailable": false, "dataPathSize": 18337792}`
)

// withUnavailable makes simctl list one usable and two unusable simulators until `delete unavailable`
// runs, after which only the usable one is left, unless the delete does nothing.
func withUnavailable(e *env, deleteWorks bool) *bool {
	deleted := false
	previous := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		switch {
		case command == "xcrun simctl delete unavailable":
			deleted = deleteWorks
			return run.Result{}
		case strings.Contains(command, "simctl list devices"):
			if deleted {
				return runtest.Out(`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [` + usableSim + `]}}`)
			}
			return runtest.Out(`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [` + usableSim + `], "com.apple.CoreSimulator.SimRuntime.iOS-27-0": [` + orphanOne + `, ` + orphanTwo + `]}}`)
		}
		return previous(command, argv, opts)
	}
	return &deleted
}

func TestListShowsTheDataSizeAndSaysWhatIsUnavailable(t *testing.T) {
	e := newEnv(t)
	withUnavailable(e, true)
	if code := e.run("list"); code != ExitOK {
		t.Fatalf("code %d, stderr %s", code, e.errs.String())
	}
	out := e.out.String()
	if !strings.Contains(out, "DATA") || !strings.Contains(out, "1.9 GB") {
		t.Errorf("the list:\n%s", out)
	}
	if strings.Contains(out, "iPhone 18 Pro") {
		t.Errorf("an unavailable simulator is listed:\n%s", out)
	}
	if !strings.Contains(e.errs.String(), "2 unavailable simulators use 1.9 GB. `lazymobile delete --unavailable` lists them") {
		t.Errorf("stderr = %q", e.errs.String())
	}
	// Without any, there is no note.
	plain := newEnv(t)
	plain.run("list")
	if strings.Contains(plain.errs.String(), "unavailable") {
		t.Errorf("a note without any: %q", plain.errs.String())
	}
}

func TestListJSONHasTheSizeAndTheUnavailableSimulators(t *testing.T) {
	e := newEnv(t)
	withUnavailable(e, true)
	if code := e.run("list", "--json"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	var got struct {
		Devices []struct {
			Name      string `json:"name"`
			DataBytes int64  `json:"data_bytes"`
		} `json:"devices"`
		Unavailable []struct {
			Name      string `json:"name"`
			DataBytes int64  `json:"data_bytes"`
			Reason    string `json:"reason"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Devices) != 1 || got.Devices[0].DataBytes != 1854255104 {
		t.Errorf("devices = %+v", got.Devices)
	}
	if len(got.Unavailable) != 2 || got.Unavailable[0].Reason != "runtime profile not found" {
		t.Errorf("unavailable = %+v", got.Unavailable)
	}
	// With none, the field is an empty list, not missing.
	plain := newEnv(t)
	plain.run("list", "--json")
	var none map[string]json.RawMessage
	if err := json.Unmarshal(plain.out.Bytes(), &none); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(string(none["unavailable"])), ""); got != "[]" {
		t.Errorf("without any, unavailable is %q", got)
	}
}

func TestDeleteUnavailableListsFirstAndDeletesNothingWithoutYes(t *testing.T) {
	e := newEnv(t)
	deleted := withUnavailable(e, true)
	code := e.run("delete", "--unavailable")
	if code != ExitUsage {
		t.Fatalf("code %d, stderr %s", code, e.errs.String())
	}
	msg := e.errs.String()
	for _, want := range []string{"delete --unavailable removes 2 unavailable simulators (1.9 GB), and their data cannot be had back", "Add --yes to confirm", "iPhone 18 Pro, iOS 27.0, 1.9 GB", "iPhone 18 Pro Max, iOS 27.0, 18 MB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stderr lacks %q:\n%s", want, msg)
		}
	}
	if *deleted || e.fake.Called("delete unavailable") {
		t.Error("something was deleted without --yes")
	}
}

func TestDeleteUnavailableWithYesDeletesThemAndSaysWhatWasFreed(t *testing.T) {
	e := newEnv(t)
	deleted := withUnavailable(e, true)
	if code := e.run("delete", "--unavailable", "--yes"); code != ExitOK {
		t.Fatalf("code %d, stderr %s", code, e.errs.String())
	}
	if got := strings.TrimSpace(e.out.String()); got != "Deleted 2 unavailable simulators, freed 1.9 GB" {
		t.Errorf("output = %q", got)
	}
	if !*deleted {
		t.Error("nothing was deleted")
	}
	// The usable simulator was not touched, and no simulator was deleted by id.
	for _, call := range e.fake.Calls() {
		if strings.HasPrefix(call, "xcrun simctl delete ") && call != "xcrun simctl delete unavailable" {
			t.Errorf("a simulator was deleted by id: %s", call)
		}
	}
	// A second time there is nothing to do.
	if code := e.run("delete", "--unavailable", "--yes"); code != ExitOK || !strings.Contains(e.out.String(), "No unavailable simulators") {
		t.Errorf("second time: code %d, %q", code, e.out.String())
	}
}

func TestDeleteUnavailableJSONAndWhatCouldNotBeDeleted(t *testing.T) {
	e := newEnv(t)
	withUnavailable(e, true)
	if code := e.run("delete", "--unavailable", "--yes", "--json"); code != ExitOK {
		t.Fatalf("code %d, stderr %s", code, e.errs.String())
	}
	var got struct {
		OK         bool `json:"ok"`
		Deleted    []struct{ Name string }
		FreedBytes int64 `json:"freed_bytes"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(got.Deleted) != 2 || got.FreedBytes != 1854255104+18337792 {
		t.Errorf("result = %+v", got)
	}

	// simctl said yes but they are still there: they are not reported as deleted.
	stuck := newEnv(t)
	withUnavailable(stuck, false)
	if code := stuck.run("delete", "--unavailable", "--yes"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if got := strings.TrimSpace(stuck.out.String()); got != "Deleted 0 unavailable simulators. 2 could not be deleted" {
		t.Errorf("stuck output = %q", got)
	}
}

func TestDeleteUnavailableTakesNoDevice(t *testing.T) {
	e := newEnv(t)
	withUnavailable(e, true)
	if code := e.run("delete", "--unavailable", "iPhone 16", "--yes"); code != ExitUsage || e.fake.Called("delete unavailable") {
		t.Errorf("code %d, calls %v", code, e.fake.Calls())
	}
}
