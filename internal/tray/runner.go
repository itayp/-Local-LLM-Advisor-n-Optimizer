package tray

import (
	"context"

	"github.com/gogpu/systray"
)

// defaultRunner is the real github.com/gogpu/systray integration — the
// only file in this package that imports it (tray.go's doc comment). It
// builds the icon and menu from opts, shows it, and blocks in the
// library's own Run() until ctx is cancelled (which calls Remove(), the
// only thing that unblocks Run()) or the user clicks Quit.
func defaultRunner(ctx context.Context, opts Options) error {
	t := systray.New()
	if len(opts.Icon.PNG) > 0 {
		t.SetIcon(opts.Icon.PNG)
	}
	if len(opts.Icon.Template) > 0 {
		t.SetTemplateIcon(opts.Icon.Template)
	}
	if opts.Tooltip != "" {
		t.SetTooltip(opts.Tooltip)
	}
	if opts.AppName != "" {
		t.SetAppName(opts.AppName)
	}

	menu := systray.NewMenu()
	haveItem := false
	if opts.Open != nil {
		menu.Add(opts.OpenLabel, opts.Open)
		haveItem = true
	}
	if opts.AutostartEnabled != nil && opts.AutostartSet != nil {
		if haveItem {
			menu.AddSeparator()
		}
		var item *systray.MenuItem
		item = menu.AddCheckbox(opts.AutostartLabel, opts.AutostartEnabled(), func() {
			newChecked, applied := autostartToggle(item.IsChecked(), opts.AutostartSet, opts.Log)
			if applied {
				item.SetChecked(newChecked)
			}
		})
		haveItem = true
	}
	if opts.Quit != nil {
		if haveItem {
			menu.AddSeparator()
		}
		menu.Add(opts.QuitLabel, opts.Quit)
	}
	t.SetMenu(menu)
	t.Show()

	// Remove() is documented safe to call from any goroutine and never
	// blocks the caller; it is what unblocks t.Run() below (there is no
	// separate Quit() despite Run()'s doc comment mentioning one — reading
	// the library's own source confirmed Remove() is it).
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			t.Remove()
		case <-stopped:
		}
	}()
	defer close(stopped)

	if err := t.Run(); err != nil {
		logWarn(opts.Log, "tray: the tray backend could not start; continuing headless", "err", err)
		return nil
	}
	return nil
}
