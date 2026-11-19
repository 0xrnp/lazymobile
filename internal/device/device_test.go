package device

import (
	"reflect"
	"testing"
)

func names(list []Device) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.Name
	}
	return out
}

func TestSortPutsReadyAndPhysicalFirst(t *testing.T) {
	list := []Device{
		{ID: "1", Name: "Zeta sim", Platform: IOS, Kind: Simulator, State: Shutdown},
		{ID: "2", Name: "Pixel", Platform: Android, Kind: Emulator, State: Booted},
		{ID: "3", Name: "alpha sim", Platform: IOS, Kind: Simulator, State: Shutdown},
		{ID: "4", Name: "My phone", Platform: Android, Kind: Physical, State: Connected},
		{ID: "5", Name: "iPhone sim", Platform: IOS, Kind: Simulator, State: Booted},
		{ID: "6", Name: "Old phone", Platform: IOS, Kind: Physical, State: Disconnected},
	}
	got := names(Sort(list))
	want := []string{"My phone", "iPhone sim", "Pixel", "Old phone", "alpha sim", "Zeta sim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if list[0].Name != "Zeta sim" {
		t.Error("Sort changed its input")
	}
}

func TestDedupeKeepsFirstAndFillsGaps(t *testing.T) {
	got := Dedupe([]Device{
		{ID: "A", Name: "First", Platform: Android, Kind: Emulator, Source: "emulator"},
		{ID: "A", Name: "Second", Platform: Android, Kind: Emulator, Serial: "emulator-5554", OSVersion: "16"},
		{ID: "A", Name: "Other kind", Platform: Android, Kind: Physical},
	})
	if len(got) != 2 {
		t.Fatalf("got %d devices, want 2", len(got))
	}
	if got[0].Name != "First" || got[0].Serial != "emulator-5554" || got[0].OSVersion != "16" {
		t.Errorf("merged device = %+v", got[0])
	}
}

func TestTargetIDPrefersTheSerialOfARunningEmulator(t *testing.T) {
	running := Device{ID: "Pixel_9", Platform: Android, Kind: Emulator, Serial: "emulator-5554"}
	if got := running.TargetID(); got != "emulator-5554" {
		t.Errorf("running emulator: %q", got)
	}
	stopped := Device{ID: "Pixel_9", Platform: Android, Kind: Emulator}
	if got := stopped.TargetID(); got != "Pixel_9" {
		t.Errorf("stopped emulator: %q", got)
	}
}

func TestMatch(t *testing.T) {
	list := []Device{
		{ID: "UDID-1", Name: "iPhone 16", Platform: IOS, Kind: Simulator},
		{ID: "UDID-2", Name: "iPhone 16 Pro", Platform: IOS, Kind: Simulator},
		{ID: "Pixel_9a", Name: "Pixel 9a", Platform: Android, Kind: Emulator, Serial: "emulator-5554"},
	}
	tests := []struct {
		name, query string
		want        []string
	}{
		{"exact name beats partial", "iphone 16", []string{"iPhone 16"}},
		{"partial matches several", "iphone", []string{"iPhone 16", "iPhone 16 Pro"}},
		{"id", "udid-2", []string{"iPhone 16 Pro"}},
		{"adb serial", "emulator-5554", []string{"Pixel 9a"}},
		{"nothing", "galaxy", []string{}},
		{"blank", "  ", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := names(Match(list, tt.query))
			if len(got) != len(tt.want) || (len(got) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Errorf("Match(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestOSLabel(t *testing.T) {
	tests := []struct {
		d    Device
		want string
	}{
		{Device{Platform: IOS, OSVersion: "18.2"}, "iOS 18.2"},
		{Device{Platform: Android, OSVersion: "16"}, "Android 16"},
		{Device{Platform: Android, OSVersion: "API 99"}, "API 99"},
		{Device{Platform: Android}, ""},
	}
	for _, tt := range tests {
		if got := tt.d.OSLabel(); got != tt.want {
			t.Errorf("OSLabel(%+v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatBytesWritesSizesAsTheFinderDoes(t *testing.T) {
	tests := map[int64]string{
		-5: "0 KB", 0: "0 KB", 1: "1 KB", 999: "1 KB", 1_000: "1 KB", 1_001: "2 KB", 340_000: "340 KB",
		999_999: "1 MB", 1_000_000: "1 MB", 18_337_792: "18 MB", 340_499_999: "340 MB", 999_000_000: "999 MB",
		1_000_000_000: "1.0 GB", 1_854_255_104: "1.9 GB", 3_700_000_000: "3.7 GB", 2_000_000_000_000: "2.0 TB",
		// Past a hundred gigabytes the decimal goes, so a size never needs more than seven columns.
		99_900_000_000: "99.9 GB", 99_960_000_000: "100 GB", 100_600_000_000: "101 GB", 999_400_000_000: "999 GB", 999_600_000_000: "1.0 TB",
	}
	for n, want := range tests {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
