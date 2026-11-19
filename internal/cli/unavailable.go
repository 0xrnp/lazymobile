package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
)

// unavailableBytes is the disk the simulators use.
func unavailableBytes(list []ios.Unavailable) int64 {
	var total int64
	for _, u := range list {
		total += u.DataBytes
	}
	return total
}

// unavailableCount is "3 unavailable simulators".
func unavailableCount(list []ios.Unavailable) string {
	return fmt.Sprintf("%d unavailable simulator%s", len(list), plural(len(list)))
}

// unavailableSummary is "3 unavailable simulators use 1.9 GB".
func unavailableSummary(list []ios.Unavailable) string {
	verb := "use"
	if len(list) == 1 {
		verb = "uses"
	}
	if total := unavailableBytes(list); total > 0 {
		return unavailableCount(list) + " " + verb + " " + device.FormatBytes(total)
	}
	return unavailableCount(list)
}

// deleteUnavailable deletes the simulators simctl cannot run. Without --yes it only says which, and
// how much disk each uses, since a deleted simulator's data cannot be had back.
func (a *App) deleteUnavailable(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.Bool("unavailable", false, "")
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return usageError{"delete --unavailable takes no device: it deletes every simulator Xcode can no longer run"}
	}
	list, err := a.Manager.ListUnavailable(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		if *asJSON {
			return a.writeJSON(map[string]any{"ok": true, "action": "delete_unavailable", "deleted": []ios.Unavailable{}, "freed_bytes": 0})
		}
		_, err := fmt.Fprintln(a.Stdout, "No unavailable simulators")
		return err
	}
	if !*yes {
		var b strings.Builder
		fmt.Fprintf(&b, "delete --unavailable removes %s", unavailableCount(list))
		if total := unavailableBytes(list); total > 0 {
			fmt.Fprintf(&b, " (%s)", device.FormatBytes(total))
		}
		b.WriteString(", and their data cannot be had back. Add --yes to confirm:")
		for _, u := range list {
			fmt.Fprintf(&b, "\n  %s", describeUnavailable(u))
		}
		return usageError{b.String()}
	}
	deleted, err := a.Manager.DeleteUnavailable(ctx, unavailableIDs(list))
	if err != nil {
		return err
	}
	freed := unavailableBytes(deleted)
	if *asJSON {
		return a.writeJSON(map[string]any{"ok": true, "action": "delete_unavailable", "deleted": deleted, "freed_bytes": freed})
	}
	text := fmt.Sprintf("Deleted %d unavailable simulator%s", len(deleted), plural(len(deleted)))
	if freed > 0 {
		text += ", freed " + device.FormatBytes(freed)
	}
	if left := len(list) - len(deleted); left > 0 {
		text += fmt.Sprintf(". %d could not be deleted", left)
	}
	_, err = fmt.Fprintln(a.Stdout, text)
	return err
}

func unavailableIDs(list []ios.Unavailable) []string {
	ids := make([]string, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	return ids
}

// describeUnavailable is "iPhone 17, iOS 27.0, 18 MB", with the system it was made for.
func describeUnavailable(u ios.Unavailable) string {
	text := u.Name
	if u.Platform != "" || u.OSVersion != "" {
		text += ", " + strings.TrimSpace(u.Platform+" "+u.OSVersion)
	}
	if u.DataBytes > 0 {
		text += ", " + device.FormatBytes(u.DataBytes)
	}
	return text
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
