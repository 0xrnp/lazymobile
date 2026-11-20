package ios

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// phoneCrashList is `devicectl device info files --domain-type systemCrashLogs` in the shape
// Xcode 27.2 wrote it for an iPhone on iOS 27, shortened: two crashes of Shop, a newer crash of
// another app, and reports that are not app crashes.
const phoneCrashList = `{"result": {"deviceIdentifier": "8FCC", "domain": "systemCrashLogs", "files": [
  {"metadata": {"extendedAttributes": {"bug_type": "309"}, "lastModDate": "2026-11-19T10:00:00.000Z", "size": 9000}, "name": "Retired/Shop-2026-11-19-100000.ips", "relativePath": "Retired/Shop-2026-11-19-100000.ips"},
  {"metadata": {"extendedAttributes": {"bug_type": "309"}, "lastModDate": "2026-11-20T10:00:00.000Z", "size": 9000}, "name": "Shop-2026-11-20-100000.ips", "relativePath": "Shop-2026-11-20-100000.ips"},
  {"metadata": {"extendedAttributes": {"bug_type": "309"}, "lastModDate": "2026-11-20T12:00:00.000Z", "size": 9000}, "name": "Other-2026-11-20-120000.ips", "relativePath": "Other-2026-11-20-120000.ips"},
  {"metadata": {"extendedAttributes": {"LogType": "211", "bug_type": "211"}, "lastModDate": "2026-11-20T13:00:00.000Z", "size": 200000}, "name": "Analytics-2026-11-20-130000.ips.ca.synced", "relativePath": "Analytics-2026-11-20-130000.ips.ca.synced"},
  {"metadata": {"extendedAttributes": {"bug_type": "298"}, "lastModDate": "2026-11-20T14:00:00.000Z", "size": 4000}, "name": "JetsamEvent-2026-11-20-140000.ips", "relativePath": "JetsamEvent-2026-11-20-140000.ips"},
  {"metadata": {"extendedAttributes": {}, "lastModDate": "2026-11-08T15:39:13.000Z", "size": 96}, "name": "Retired", "relativePath": "Retired", "resources": {"isDirectory": true}}
]}}`

// phoneFake answers devicectl for a phone whose crash logs are phoneCrashList, and whose apps are
// apps. The reports it copies are a crash of the app named in their file name.
func phoneFake(t *testing.T, apps string) *runtest.Fake {
	return &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		output := argv[len(argv)-1]
		write := func(path, text string) {
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Error(err)
			}
		}
		switch {
		case strings.Contains(command, "device info files"):
			write(output, phoneCrashList)
		case strings.Contains(command, "device info apps"):
			write(output, apps)
		case strings.Contains(command, "device copy from"):
			source := argv[slices.Index(argv, "--source")+1]
			bundleID := "com.example.other"
			if strings.Contains(source, "Shop-") {
				bundleID = "com.example.shop"
			}
			write(argv[slices.Index(argv, "--destination")+1], ips("309", bundleID, "", source))
			write(output, `{"result": {}}`)
		default:
			return run.Result{Missing: true}
		}
		return run.Result{}
	}}
}

const shopApps = `{"result": {"apps": [{"bundleIdentifier": "com.example.shop", "name": "Shop", "url": "file:///private/var/containers/Bundle/Application/1A2B/Shop.app/"}]}}`

func copies(fake *runtest.Fake) []string {
	var sources []string
	for _, call := range fake.Calls() {
		if strings.Contains(call, "device copy from") {
			_, after, _ := strings.Cut(call, "--source ")
			source, _, _ := strings.Cut(after, " ")
			sources = append(sources, source)
		}
	}
	return sources
}

func TestAnIPhonesCrashIsCopiedFromItsCrashLogs(t *testing.T) {
	fake := phoneFake(t, shopApps)
	c := New(fake)
	c.PhoneCrashDir = t.TempDir()
	report, err := c.LatestCrash(context.Background(), iphone, "com.example.shop")
	if err != nil {
		t.Fatal(err)
	}
	// The newest report named after Shop is the only one copied, though another app's is newer.
	if got := copies(fake); !slices.Equal(got, []string{"Shop-2026-11-20-100000.ips"}) {
		t.Errorf("copied %q", got)
	}
	if report.Path != filepath.Join(c.PhoneCrashDir, iphone.ID, "Shop-2026-11-20-100000.ips") || !strings.Contains(report.Text, "com.example.shop") {
		t.Errorf("report %+v", report)
	}
	if info, err := os.Stat(filepath.Dir(report.Path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the folder of copies: %v, %v", info.Mode(), err)
	}
	// A report copied before is read from the copy.
	if _, err := c.LatestCrash(context.Background(), iphone, "com.example.shop"); err != nil || len(copies(fake)) != 1 {
		t.Errorf("asked again: %v, copied %q", err, copies(fake))
	}
}

func TestAnIPhonesCrashIsFoundWhenTheExecutableIsNotKnown(t *testing.T) {
	fake := phoneFake(t, `{"result": {"apps": []}}`)
	c := New(fake)
	c.PhoneCrashDir = t.TempDir()
	if _, err := c.LatestCrash(context.Background(), iphone, "com.example.shop"); err != nil {
		t.Fatal(err)
	}
	if got := copies(fake); !slices.Equal(got, []string{"Other-2026-11-20-120000.ips", "Shop-2026-11-20-100000.ips"}) {
		t.Errorf("copied %q", got)
	}
}

func TestAnIPhoneWithoutACrashOfTheAppCopiesNothingItNeedNot(t *testing.T) {
	fake := phoneFake(t, shopApps)
	c := New(fake)
	c.PhoneCrashDir = t.TempDir()
	_, err := c.LatestCrash(context.Background(), iphone, "com.example.missing")
	if err == nil || !strings.Contains(err.Error(), "No crash of com.example.missing found on Phone") {
		t.Errorf("error %v", err)
	}
	// Only app crashes are copied, never the analytics or memory reports.
	for _, source := range copies(fake) {
		if !strings.Contains(source, "Shop-") && !strings.Contains(source, "Other-") {
			t.Errorf("copied %s", source)
		}
	}
}

func TestParsePhoneCrashFilesKeepsAppCrashesNewestFirst(t *testing.T) {
	files, err := ParsePhoneCrashFiles(phoneCrashList)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{"Other-2026-11-20-120000.ips", "Shop-2026-11-20-100000.ips", "Retired/Shop-2026-11-19-100000.ips"}
	if !slices.Equal(paths, want) {
		t.Errorf("%q, want %q", paths, want)
	}
	if _, err := ParsePhoneCrashFiles("not json"); err == nil {
		t.Error("not JSON was read")
	}
}

func TestAFailedCopyFromAnIPhoneIsNotKeptAndTheNextReportIsRead(t *testing.T) {
	inner := phoneFake(t, shopApps)
	fails := true
	fake := &runtest.Fake{Answer: func(command string, argv []string, opts run.Options) run.Result {
		if fails && strings.Contains(command, "--source Shop-2026-11-20-100000.ips") {
			// The copy stops halfway, as when the phone is unplugged or the report was rotated away.
			if err := os.WriteFile(argv[slices.Index(argv, "--destination")+1], []byte(`{"bug_type": "30`), 0o600); err != nil {
				t.Error(err)
			}
			return runtest.Fail(1, "The file does not exist")
		}
		return inner.Answer(command, argv, opts)
	}}
	c := New(fake)
	c.PhoneCrashDir = t.TempDir()
	report, err := c.LatestCrash(context.Background(), iphone, "com.example.shop")
	if err != nil || !strings.HasSuffix(report.Path, "Shop-2026-11-19-100000.ips") {
		t.Fatalf("report %+v, %v", report, err)
	}
	dir := filepath.Join(c.PhoneCrashDir, iphone.ID)
	for _, name := range []string{"Shop-2026-11-20-100000.ips", "Shop-2026-11-20-100000.ips.part"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
	// Once the phone answers, the newer report is copied and read.
	fails = false
	if report, err := c.LatestCrash(context.Background(), iphone, "com.example.shop"); err != nil || !strings.HasSuffix(report.Path, "Shop-2026-11-20-100000.ips") {
		t.Errorf("asked again: %+v, %v", report, err)
	}
}

func TestEveryCopyFromAnIPhoneFailingSaysWhy(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "device copy from") {
			return runtest.Fail(1, "The device was disconnected")
		}
		return phoneFake(t, shopApps).Answer(command, argv, opts)
	}}
	c := New(fake)
	c.PhoneCrashDir = t.TempDir()
	if _, err := c.LatestCrash(context.Background(), iphone, "com.example.shop"); apperr.CodeOf(err) != apperr.CommandFailed {
		t.Errorf("error %v, want the copy's failure", err)
	}
}
