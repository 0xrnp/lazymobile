package ios

import (
	"reflect"
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
