package ios

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// phoneCrashCopies is how many reports of each kind phoneCrash copies at most while looking for
// the app's: those named after the app, then the others. Each copy takes a few seconds.
const phoneCrashCopies = 5

// PhoneCrashFile is an app crash report on an iPhone, as devicectl lists it.
type PhoneCrashFile struct {
	// Path is where it is in the phone's crash logs, such as Retired/Runner-2026-11-20-101500.ips.
	Path string
	// Modified is when it was written, as devicectl says it (RFC 3339, so it sorts as text).
	Modified string
}

// phoneCrash returns the newest crash report of an app on a connected iPhone. devicectl lists the
// phone's crash logs with their kind, so only app crashes are copied: the newest named after the
// app's executable first, then a few others, in case the executable is named differently. Copies
// are kept, in a folder only this user can open, so asking again does not copy them again. Nothing
// is removed from the phone.
func (c *Client) phoneCrash(ctx context.Context, d device.Device, bundleID string) (device.CrashReport, error) {
	if !d.Ready() {
		return device.CrashReport{}, apperr.New(apperr.InvalidState, "%s is not connected", d.Name).
			WithHint("Connect it with a cable, or on the same Wi-Fi, and unlock it.")
	}
	listing, err := c.devicectl(ctx, "device", "info", "files", "--device", d.ID, "--domain-type", "systemCrashLogs")
	if err != nil {
		return device.CrashReport{}, err
	}
	files, err := ParsePhoneCrashFiles(listing)
	if err != nil {
		return device.CrashReport{}, err
	}
	var dir string
	var copyErr error
	for _, f := range c.phoneCrashOrder(ctx, d, bundleID, files) {
		if dir == "" {
			if dir, err = c.phoneCrashDir(d); err != nil {
				return device.CrashReport{}, err
			}
		}
		if ctx.Err() != nil {
			return device.CrashReport{}, ctx.Err()
		}
		local := filepath.Join(dir, path.Base(f.Path))
		if _, err := os.Stat(local); err != nil {
			if err := c.copyPhoneCrash(ctx, d, f.Path, local); err != nil {
				// The phone may have removed this report since it was listed. Look at the others.
				if ctx.Err() != nil {
					return device.CrashReport{}, ctx.Err()
				}
				if copyErr == nil {
					copyErr = err
				}
				continue
			}
		}
		data, err := os.ReadFile(local)
		if err != nil {
			continue
		}
		if report, ok := parseCrash(data, bundleID, func(ipsBody) bool { return true }); ok {
			report.Path = local
			return report, nil
		}
	}
	if copyErr != nil {
		return device.CrashReport{}, copyErr
	}
	return device.CrashReport{}, apperr.New(apperr.NotFound, "No crash of %s found on %s", bundleID, d.Name).
		WithHint("An iPhone keeps crash reports only for a while. On the phone, Settings > Privacy & Security > Analytics & Improvements > Analytics Data lists the ones it has.")
}

// copyPhoneCrash copies one crash report from d to local. It is copied under another name first, so
// a copy that stops halfway is not kept as the report.
func (c *Client) copyPhoneCrash(ctx context.Context, d device.Device, source, local string) error {
	part := local + ".part"
	if _, err := c.devicectl(ctx, "device", "copy", "from", "--device", d.ID, "--domain-type", "systemCrashLogs",
		"--source", source, "--destination", part); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, local); err != nil {
		_ = os.Remove(part)
		return apperr.New(apperr.CommandFailed, "Could not keep the crash report copied to %s: %v", local, err)
	}
	return nil
}

// phoneCrashOrder is the order in which to look through files for a crash of bundleID: the newest
// named after its executable, then the newest others, phoneCrashCopies of each at most.
func (c *Client) phoneCrashOrder(ctx context.Context, d device.Device, bundleID string, files []PhoneCrashFile) []PhoneCrashFile {
	executable := ""
	if apps, err := c.devicectl(ctx, "device", "info", "apps", "--device", d.ID); err == nil {
		if folder, err := ParseAppFolder(apps, bundleID); err == nil {
			// An app's executable is almost always named after its .app folder.
			executable = strings.TrimSuffix(path.Base(strings.TrimSuffix(folder, "/")), ".app")
		}
	}
	var named, others []PhoneCrashFile
	for _, f := range files {
		if executable != "" && strings.HasPrefix(path.Base(f.Path), executable+"-") {
			if len(named) < phoneCrashCopies {
				named = append(named, f)
			}
		} else if len(others) < phoneCrashCopies {
			others = append(others, f)
		}
	}
	return append(named, others...)
}

// phoneCrashDir is the folder the crash reports of d are copied into, made if needed.
func (c *Client) phoneCrashDir(d device.Device) (string, error) {
	root := c.PhoneCrashDir
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", apperr.New(apperr.CommandFailed, "Could not find the cache folder: %v", err)
		}
		root = filepath.Join(cache, "lazymobile", "iphone-crashes")
	}
	// A crash report can hold personal data, so only this user can open the folder.
	dir := filepath.Join(root, d.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not make %s: %v", dir, err)
	}
	return dir, nil
}

// ParsePhoneCrashFiles reads `devicectl device info files --domain-type systemCrashLogs
// --json-output`, and returns the app crash reports in it, newest first.
func ParsePhoneCrashFiles(text string) ([]PhoneCrashFile, error) {
	var data struct {
		Result struct {
			Files []struct {
				RelativePath string `json:"relativePath"`
				Metadata     struct {
					ExtendedAttributes map[string]any `json:"extendedAttributes"`
					LastModDate        string         `json:"lastModDate"`
				} `json:"metadata"`
			} `json:"files"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the crash log list from devicectl")
	}
	var files []PhoneCrashFile
	for _, f := range data.Result.Files {
		if f.Metadata.ExtendedAttributes["bug_type"] == crashBugType && path.Ext(f.RelativePath) == ".ips" {
			files = append(files, PhoneCrashFile{Path: f.RelativePath, Modified: f.Metadata.LastModDate})
		}
	}
	slices.SortStableFunc(files, func(a, b PhoneCrashFile) int { return strings.Compare(b.Modified, a.Modified) })
	return files, nil
}
