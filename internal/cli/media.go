package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

// media puts photos and videos into the library of a simulator or emulator.
func (a *App) media(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("media", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return usageError{"media needs a device and one or more photo or video files"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	files := positional[1:]
	placed, err := a.Manager.AddMedia(ctx, d, files)
	if err != nil {
		return err
	}
	sentence := fmt.Sprintf("Added %d file(s) to %s", len(files), d.Name)
	for _, p := range placed {
		sentence += "\n  " + p
	}
	return a.done(*asJSON, result{Action: "media", Device: d, Files: placed}, sentence)
}

// clipboard sets or reads the text on a simulator's clipboard.
func (a *App) clipboard(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("clipboard", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	get := fs.Bool("get", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	switch {
	case *get && len(positional) != 1, !*get && len(positional) != 2:
		return usageError{"clipboard needs a device and the text to copy, or a device and --get"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if *get {
		text, err := a.Manager.Clipboard(ctx, d)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(map[string]any{"ok": true, "action": "clipboard_get", "device": d, "text": text})
		}
		_, err = fmt.Fprintln(a.Stdout, text)
		return err
	}
	text := positional[1]
	if text == "-" {
		data, err := io.ReadAll(io.LimitReader(a.Stdin, 1<<20+1))
		if err != nil {
			return apperr.New(apperr.CommandFailed, "Could not read the text: %v", err)
		}
		text = string(data)
	}
	if err := a.Manager.SetClipboard(ctx, d, text); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "clipboard", Device: d}, "Copied the text to the clipboard of "+d.Name)
}
