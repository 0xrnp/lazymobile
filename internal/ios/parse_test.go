package ios

import (
	"reflect"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

func byName(list []device.Device) map[string]device.Device {
	out := map[string]device.Device{}
	for _, d := range list {
		out[d.Name] = d
	}
	return out
}

func TestParseSimctl(t *testing.T) {
	devices, err := ParseSimctl(simctlFixture())
	if err != nil {
		t.Fatal(err)
	}
	got := byName(devices)

	want := map[string]struct {
		state   device.State
		version string
	}{
		"iPhone 16 Pro":         {device.Shutdown, "18.2"},
		"iPhone 16":             {device.Booted, "18.2"},
		"iPad Pro 13-inch (M4)": {device.ShuttingDown, "18.2"},
		"iPhone 16 Plus":        {device.Booting, "18.2"},
		"Brand New":             {device.Unknown, "18.2"},
		"iPhone 15":             {device.Shutdown, "17.5"},
		"iPhone 17":             {device.Shutdown, "18.2"},
		"Old Style Available":   {device.Shutdown, "16.4"},
		"Ancient":               {device.Shutdown, "12.1"},
	}
	if len(got) != len(want) {
		t.Errorf("got %d devices %v, want %d", len(got), reflect.ValueOf(got).MapKeys(), len(want))
	}
	for name, w := range want {
		d, found := got[name]
		if !found {
			t.Errorf("%s is missing", name)
			continue
		}
		if d.State != w.state || d.OSVersion != w.version {
			t.Errorf("%s: state %s version %s, want %s %s", name, d.State, d.OSVersion, w.state, w.version)
		}
		if d.Platform != device.IOS || d.Kind != device.Simulator || d.Source != "simctl" || d.Transport != "local" {
			t.Errorf("%s: wrong fixed fields %+v", name, d)
		}
	}
}

func TestParseSimctlRejectsUnreadableOutput(t *testing.T) {
	for _, text := range []string{"", "not json", `{"devices": 5}`, `{}`, `[]`} {
		if _, err := ParseSimctl(text); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("ParseSimctl(%q) error = %v, want parse_failed", text, err)
		}
	}
}

func TestParseDevicectlReadsEveryLayout(t *testing.T) {
	for _, layout := range []string{"new", "old", "both"} {
		t.Run(layout, func(t *testing.T) {
			devices, err := ParseDevicectl(devicectlFixture(layout))
			if err != nil {
				t.Fatal(err)
			}
			got := byName(devices)
			if len(got) != 5 {
				t.Fatalf("got %d devices, want 5: %+v", len(got), devices)
			}
			tests := []struct {
				name      string
				state     device.State
				transport string
				version   string
			}{
				{"Test iPhone", device.Connected, "usb", "18.2"},
				{"Test iPad", device.Connected, "wifi", "17.6"},
				{"Offline iPhone", device.Disconnected, "", "18.2"},
				{"Untrusted iPhone", device.Unauthorized, "usb", "18.2"},
				{"No Dev Mode iPhone", device.Connected, "usb", "18.2"},
			}
			for _, tt := range tests {
				d := got[tt.name]
				if d.State != tt.state || d.Transport != tt.transport || d.OSVersion != tt.version {
					t.Errorf("%s: state %s transport %q version %q, want %s %q %q",
						tt.name, d.State, d.Transport, d.OSVersion, tt.state, tt.transport, tt.version)
				}
				if d.Kind != device.Physical || d.Source != "devicectl" {
					t.Errorf("%s: wrong fixed fields %+v", tt.name, d)
				}
			}
			if got["No Dev Mode iPhone"].Detail == "" {
				t.Error("a device with Developer Mode off should say so")
			}
			if got["Test iPhone"].Detail != "" {
				t.Error("a healthy device should have no detail")
			}
		})
	}
}

func TestParseDevicectlRejectsUnreadableOutput(t *testing.T) {
	for _, text := range []string{"", "nope", `{"result": {}}`, `{"result": 3}`} {
		if _, err := ParseDevicectl(text); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("ParseDevicectl(%q) error = %v, want parse_failed", text, err)
		}
	}
}

func TestParseRuntimes(t *testing.T) {
	text := encode(object{"runtimes": []object{
		{"identifier": runtimePrefix + "iOS-17-5", "name": "iOS 17.5", "version": "17.5", "platform": "iOS", "isAvailable": true},
		{"identifier": runtimePrefix + "iOS-18-10", "name": "iOS 18.10", "version": "18.10", "platform": "iOS", "isAvailable": true,
			"supportedDeviceTypes": []object{
				{"identifier": "ipad-air", "name": "iPad Air", "productFamily": "iPad"},
				{"identifier": "watch", "name": "Apple Watch", "productFamily": "Apple Watch"},
				{"identifier": "iphone-16", "name": "iPhone 16", "productFamily": "iPhone"},
			}},
		{"identifier": runtimePrefix + "iOS-18-2", "name": "iOS 18.2", "version": "18.2", "platform": "iOS", "isAvailable": true},
		{"identifier": runtimePrefix + "iOS-18-2-special", "name": "iOS 18.2 special", "version": "18.2", "platform": "iOS", "isAvailable": true},
		{"identifier": runtimePrefix + "iOS-16-0", "name": "iOS 16.0", "version": "16.0", "platform": "iOS", "isAvailable": false},
		{"identifier": runtimePrefix + "tvOS-18-2", "name": "tvOS 18.2", "version": "18.2", "platform": "tvOS", "isAvailable": true},
		// Before Xcode 13: no platform field.
		{"identifier": runtimePrefix + "iOS-12-1", "name": "iOS 12.1", "version": "12.1", "availability": "(available)"},
		{"identifier": runtimePrefix + "watchOS-5-1", "name": "watchOS 5.1", "version": "5.1", "availability": "(available)"},
	}})
	runtimes, err := ParseRuntimes(text)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runtimes {
		got = append(got, r.Name)
	}
	// 18.10 is newer than 18.2: versions compare as numbers, not text.
	want := []string{"iOS 18.10", "iOS 18.2", "iOS 18.2 special", "iOS 17.5", "iOS 12.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runtimes = %v, want %v", got, want)
	}
	wantTypes := []Choice{{"iphone-16", "iPhone 16"}, {"ipad-air", "iPad Air"}}
	if !reflect.DeepEqual(runtimes[0].DeviceTypes, wantTypes) {
		t.Errorf("device types = %v, want %v", runtimes[0].DeviceTypes, wantTypes)
	}
}

func TestParseDeviceTypesFallsBackToNames(t *testing.T) {
	text := encode(object{"devicetypes": []object{
		{"identifier": "tv", "name": "Apple TV"},
		{"identifier": "ipad", "name": "iPad (5th generation)"},
		{"identifier": "phone", "name": "iPhone X"},
	}})
	want := []Choice{{"phone", "iPhone X"}, {"ipad", "iPad (5th generation)"}}
	if got := ParseDeviceTypes(text); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDevicectlAppsKeepsVersionAndBuildInIDOrder(t *testing.T) {
	apps, err := ParseDevicectlApps(devicectlAppsFixture())
	if err != nil {
		t.Fatal(err)
	}
	want := []device.App{
		{ID: "com.example.app.staging", Name: "Example Staging", Version: "1.4.2", Build: "87"},
		{ID: "com.example.preview", Name: "Example Preview", Version: "2.0.0", Build: "1"},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Errorf("apps = %+v\nwant   %+v", apps, want)
	}
}

func TestParseDevicectlAppsOfADeviceWithNone(t *testing.T) {
	apps, err := ParseDevicectlApps(`{"result": {"apps": []}}`)
	if err != nil || apps == nil || len(apps) != 0 {
		t.Errorf("apps = %#v, err = %v, want an empty list", apps, err)
	}
}

func TestParseDevicectlAppsRejectsUnreadableOutput(t *testing.T) {
	// The last is what devicectl writes when it fails: an outcome and an error, with no result.
	for _, text := range []string{"", "nope", `{"result": {}}`, `{"result": null, "info": {"outcome": "failed"}}`} {
		if _, err := ParseDevicectlApps(text); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("ParseDevicectlApps(%q) error = %v, want parse_failed", text, err)
		}
	}
}

// withSize adds the folder size and an unavailable reason, as newer simctl reports them.
func withSize(entry object, bytes int64, reason string) object {
	entry["dataPathSize"] = bytes
	if reason != "" {
		entry["isAvailable"] = false
		entry["availabilityError"] = reason
	}
	return entry
}

func sizedFixture() string {
	return encode(object{"devices": object{
		runtimePrefix + "iOS-18-2": []object{
			withSize(sim(udidShutdown, "iPhone 16 Pro", "Shutdown"), 1_854_255_104, ""),
			sim(udidBooted, "iPhone 16", "Booted"),
		},
		runtimePrefix + "iOS-27-0": []object{
			withSize(sim("00000000-0000-4000-8000-0000000000D1", "Orphan", "Shutdown"), 18_337_792, "runtime profile not found using “System” match policy"),
			withSize(sim("00000000-0000-4000-8000-0000000000D2", "Orphan Two", "Shutdown"), 1_000, ""),
		},
		runtimePrefix + "watchOS-11-2": []object{withSize(sim("00000000-0000-4000-8000-0000000000F1", "Apple Watch", "Shutdown"), 5, "gone")},
		// Older simctl: an availability string, and no sizes at all.
		runtimePrefix + "iOS-16-4": []any{
			object{"udid": "00000000-0000-4000-8000-0000000000E2", "name": "Old Unavailable", "state": "Shutdown", "availability": "(unavailable, runtime profile not found)"},
		},
	}})
}

func TestParseSimctlReadsTheDataSize(t *testing.T) {
	devices, err := ParseSimctl(sizedFixture())
	if err != nil {
		t.Fatal(err)
	}
	got := byName(devices)
	if got["iPhone 16 Pro"].DataBytes != 1_854_255_104 {
		t.Errorf("iPhone 16 Pro: %d bytes", got["iPhone 16 Pro"].DataBytes)
	}
	// A simctl that does not report sizes leaves it unknown, not zero bytes of data.
	if got["iPhone 16"].DataBytes != 0 {
		t.Errorf("iPhone 16: %d bytes", got["iPhone 16"].DataBytes)
	}
	if _, listed := got["Orphan"]; listed {
		t.Error("an unavailable simulator is listed")
	}
}

func TestParseUnavailableListsOnlyUnusableIOSSimulatorsWithTheirSizeAndReason(t *testing.T) {
	list, err := ParseUnavailable(sizedFixture())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Unavailable{}
	for _, u := range list {
		byID[u.Name] = u
	}
	if len(list) != 3 {
		t.Fatalf("got %d: %+v", len(list), list)
	}
	orphan := byID["Orphan"]
	if orphan.DataBytes != 18_337_792 || orphan.OSVersion != "27.0" || orphan.Platform != "iOS" || !strings.Contains(orphan.Reason, "runtime profile not found") {
		t.Errorf("orphan = %+v", orphan)
	}
	if old := byID["Old Unavailable"]; old.DataBytes != 0 || old.OSVersion != "16.4" || old.Platform != "iOS" {
		t.Errorf("old = %+v", old)
	}
	// An unusable watch is listed too, since deleting the unavailable ones deletes it, but it is
	// not an iOS simulator to run.
	if watch := byID["Apple Watch"]; watch.Platform != "watchOS" || watch.OSVersion != "11.2" || watch.DataBytes != 5 {
		t.Errorf("watch = %+v", watch)
	}
	devices, _ := ParseSimctl(sizedFixture())
	if _, listed := byName(devices)["Apple Watch"]; listed {
		t.Error("a watch is in the device list")
	}
}

func TestParseUnavailableKeepsEveryPlatformAndRuntimesItDoesNotKnow(t *testing.T) {
	text := encode(object{"devices": object{
		runtimePrefix + "tvOS-18-2":  []object{withSize(sim("T1", "Apple TV", "Shutdown"), 7, "gone")},
		runtimePrefix + "xrOS-2-2":   []object{withSize(sim("V1", "Apple Vision Pro", "Shutdown"), 9, "gone")},
		"some-future-runtime":        []object{withSize(sim("F1", "Future Thing", "Shutdown"), 11, "gone")},
		runtimePrefix + "iOS-18-2":   []object{withSize(object{"udid": "N1", "state": "Shutdown", "isAvailable": false}, 13, "gone")},
		runtimePrefix + "watchOS-11": []object{sim("W1", "Fine Watch", "Shutdown")},
	}})
	list, err := ParseUnavailable(text)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Unavailable{}
	for _, u := range list {
		got[u.ID] = u
	}
	if len(list) != 4 {
		t.Fatalf("got %d: %+v", len(list), list)
	}
	if got["T1"].Platform != "tvOS" || got["V1"].Platform != "visionOS" || got["V1"].OSVersion != "2.2" {
		t.Errorf("tv %+v, vision %+v", got["T1"], got["V1"])
	}
	// A runtime lazymobile does not know is still listed, without a system.
	if f := got["F1"]; f.Platform != "" || f.OSVersion != "" || f.Name != "Future Thing" {
		t.Errorf("future = %+v", f)
	}
	// A simulator without a name is named by its id, so it is not deleted unseen.
	if got["N1"].Name != "N1" {
		t.Errorf("unnamed = %+v", got["N1"])
	}
	// One that can run is not listed.
	if _, ok := got["W1"]; ok {
		t.Error("a usable watch is listed")
	}
}

func TestParseUnavailableRejectsUnreadableOutput(t *testing.T) {
	if _, err := ParseUnavailable("not json"); apperr.CodeOf(err) != apperr.ParseFailed {
		t.Errorf("error = %v", err)
	}
}
