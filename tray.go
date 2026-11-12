package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"sync"
	"time"

	"fyne.io/systray"
)

var (
	channelPorts = []int{11988, 11989, 11990, 11991}
	sensitivity  = []struct {
		key  string
		gain float64
	}{{"sens.low", 0.6}, {"sens.normal", 1.0}, {"sens.high", 1.6}, {"sens.veryhigh", 2.5}}
	languages = []struct{ setting, title string }{ // language names are shown in their own language
		{langAuto, ""}, {langEN, "English"}, {langPL, "Polski"},
	}
)

// radio is a group of checkbox menu items where exactly one is checked.
type radio []*systray.MenuItem

func (r radio) set(i int) {
	for j, item := range r {
		if j == i {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

func onClick(item *systray.MenuItem, fn func()) {
	go func() {
		for range item.ClickedCh {
			fn()
		}
	}()
}

// labels remembers how to title each menu item, so switching language relabels the menu.
type labels struct {
	mu    sync.Mutex
	items []func()
}

func (l *labels) add(item *systray.MenuItem, title, tip func() string) *systray.MenuItem {
	apply := func() {
		item.SetTitle(title())
		if tip != nil {
			item.SetTooltip(tip())
		}
	}
	l.mu.Lock()
	l.items = append(l.items, apply)
	l.mu.Unlock()
	return item
}

func (l *labels) refresh() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, apply := range l.items {
		apply()
	}
}

func text(key string) func() string { return func() string { return T(key) } }

// choiceMenu is a submenu with an "automatic" first item plus options that are refreshed in the
// background (the audio devices). Exactly one item is checked: the current value, or none if the
// saved value isn't available right now.
type choiceMenu struct {
	mu     sync.Mutex
	parent *systray.MenuItem
	items  radio
	values []string // values[0] = "" (automatic)
	titles []string
	pick   func(value string)
}

func newChoiceMenu(parent *systray.MenuItem, lbl *labels, autoKey string, pick func(value string)) *choiceMenu {
	c := &choiceMenu{parent: parent, values: []string{""}, titles: []string{""}, pick: pick}
	first := lbl.add(parent.AddSubMenuItemCheckbox(T(autoKey), "", false), text(autoKey), nil)
	c.items = radio{first}
	onClick(first, func() { c.choose(0) })
	return c
}

func (c *choiceMenu) choose(i int) {
	c.mu.Lock()
	value := c.values[i]
	c.items.set(i)
	c.mu.Unlock()
	c.pick(value)
}

// update shows the given options (values with their menu titles) and checks current.
func (c *choiceMenu) update(values, titles []string, current string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values, c.titles = append([]string{""}, values...), append([]string{""}, titles...)
	for i, title := range titles {
		if i+1 < len(c.items) {
			c.items[i+1].SetTitle(title)
			c.items[i+1].Show()
		} else {
			item := c.parent.AddSubMenuItemCheckbox(title, "", false)
			c.items = append(c.items, item)
			idx := i + 1
			onClick(item, func() { c.choose(idx) })
		}
	}
	for _, item := range c.items[len(c.values):] {
		item.Hide()
	}
	checked := slices.Index(c.values, current) // -1: saved choice not available right now
	for i, item := range c.items[:len(c.values)] {
		if i == checked {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

// changed reports whether the options differ from what's shown.
func (c *choiceMenu) changed(values, titles []string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !slices.Equal(values, c.values[1:]) || !slices.Equal(titles, c.titles[1:])
}

// runTray runs the tray until Quit. justUpdated: this copy was started by an update.
func runTray(eng *Engine, justUpdated bool) {
	ctx, stop := context.WithCancel(context.Background())
	followSystemMenuTheme() // before any menu exists
	systray.Run(func() { trayReady(ctx, stop, eng, justUpdated) }, stop)
}

func trayReady(ctx context.Context, stop func(), eng *Engine, justUpdated bool) {
	s := eng.Settings()
	setLanguage(s.Language)
	var lbl labels

	// The icon is redrawn for the current state and taskbar theme (dark/light).
	var iconMu sync.Mutex
	iconState, dark := StateStarting, darkTaskbar()
	setIcon := func(state State, isDark bool) {
		iconMu.Lock()
		defer iconMu.Unlock()
		iconState, dark = state, isDark
		systray.SetIcon(trayIcon(state, isDark))
	}
	setIcon(StateStarting, dark)
	systray.SetTooltip("WLED Audio Sync")
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			isDark := darkTaskbar()
			iconMu.Lock()
			changed, state := isDark != dark, iconState
			iconMu.Unlock()
			if changed {
				themeChanged()
				setIcon(state, isDark)
			}
		}
	}()

	// Status lines, refreshed from the engine (and on language change).
	var stMu sync.Mutex
	status := Status{State: StateStarting}
	mStatus := systray.AddMenuItem("", "")
	mStatus.Disable()
	mTarget := systray.AddMenuItem("", "")
	mTarget.Disable()
	showStatus := func() {
		stMu.Lock()
		st := status
		stMu.Unlock()
		text := st.Text()
		systray.SetTooltip("WLED Audio Sync: " + text)
		mStatus.SetTitle(text)
		switch {
		case st.Device != "":
			mTarget.SetTitle(fmt.Sprintf("%s → %s", st.Device, st.Target))
		case st.Target != "":
			mTarget.SetTitle("→ " + st.Target)
		}
	}
	showStatus()

	save := func() {
		if err := saveSettings(eng.Settings()); err != nil {
			log.Printf("saving settings: %v", err)
		}
	}

	// "Update available": hidden until a check finds a newer release that isn't skipped.
	upd := newUpdater()
	mUpdate := systray.AddMenuItem("", "")
	mUpdInstall := mUpdate.AddSubMenuItem("", "")
	mUpdNotes := lbl.add(mUpdate.AddSubMenuItem(T("upd.notes"), ""), text("upd.notes"), nil)
	mUpdSkip := lbl.add(mUpdate.AddSubMenuItem(T("upd.skip"), ""), text("upd.skip"), nil)
	mUpdate.Hide()
	showUpdate := func() {
		r := upd.Available()
		if r == nil || r.Version() == eng.Settings().SkipVersion {
			mUpdate.Hide()
			return
		}
		mUpdate.SetTitle(fmt.Sprintf(T("upd.available"), r.Version()))
		if upd.CanInstall(r) {
			mUpdInstall.SetTitle(T("upd.install"))
		} else {
			mUpdInstall.SetTitle(T("upd.download"))
		}
		mUpdate.Show()
	}
	upd.onChange = func() { // a background check found a new release
		showUpdate()
		r, s := upd.Available(), eng.Settings()
		if r == nil || r.Version() == s.SkipVersion || r.Version() == s.NotifiedVersion {
			return // one notification per version, none for skipped ones
		}
		eng.Update(func(s *Settings) { s.NotifiedVersion = r.Version() })
		save()
		notify(fmt.Sprintf(T("upd.notify"), r.Version()))
	}
	onClick(mUpdInstall, func() {
		r := upd.Available()
		if r == nil {
			return
		}
		if !upd.CanInstall(r) {
			_ = openURL(r.Page)
			return
		}
		mUpdInstall.Disable()
		mUpdate.SetTitle(T("upd.installing"))
		exe, err := upd.Install(ctx, r)
		if err != nil {
			log.Printf("installing update %s: %v", r.Version(), err)
			notify(T("upd.installfailed"))
			mUpdInstall.Enable()
			showUpdate()
			return
		}
		log.Printf("installed update %s, restarting", r.Version())
		if err := restartUpdated(exe); err != nil {
			log.Printf("restarting after update: %v", err)
			notify(T("upd.restart"))
			mUpdate.Hide()
			return
		}
		stop()
		systray.Quit()
	})
	onClick(mUpdNotes, func() {
		if r := upd.Available(); r != nil {
			_ = openURL(r.Page)
		}
	})
	onClick(mUpdSkip, func() {
		if r := upd.Available(); r != nil {
			eng.Update(func(s *Settings) { s.SkipVersion = r.Version() })
			save()
		}
		showUpdate()
	})
	go upd.Run(ctx, func() bool { return !eng.Settings().NoUpdateCheck })
	if justUpdated {
		notify(fmt.Sprintf(T("upd.done"), version))
	}
	systray.AddSeparator()

	mPause := lbl.add(systray.AddMenuItemCheckbox(T("pause"), T("pause.tip"), false), text("pause"), text("pause.tip"))
	onClick(mPause, func() {
		paused := !mPause.Checked()
		if paused {
			mPause.Check()
		} else {
			mPause.Uncheck()
		}
		eng.SetPaused(paused)
	})

	// Audio source: "default output" + every output device (list refreshed in the background).
	mSource := lbl.add(systray.AddMenuItem(T("source"), T("source.tip")), text("source"), text("source.tip"))
	sources := newChoiceMenu(mSource, &lbl, "source.default", func(name string) {
		eng.Update(func(s *Settings) { s.Device = name })
		save()
	})
	refreshLists := func(force bool) {
		if names, err := listOutputDevices(); force || (err == nil && sources.changed(names, names)) {
			sources.update(names, names, eng.Settings().Device) // on error: just "default output"
		}
	}
	refreshLists(true)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refreshLists(false)
			}
		}
	}()

	// Channel = WLED AudioReactive sync port; lets different PCs drive different lights.
	mChannel := lbl.add(systray.AddMenuItem(T("channel"), T("channel.tip")), text("channel"), text("channel.tip"))
	ports := slices.Clone(channelPorts)
	if !slices.Contains(ports, s.Port) {
		ports = append(ports, s.Port)
	}
	var chItems radio
	for i, port := range ports {
		title := func() string {
			if port == defaultPort {
				return strconv.Itoa(port) + T("channel.default")
			}
			return strconv.Itoa(port)
		}
		item := lbl.add(mChannel.AddSubMenuItemCheckbox(title(), "", port == s.Port), title, nil)
		chItems = append(chItems, item)
		onClick(item, func() {
			chItems.set(i)
			eng.Update(func(s *Settings) { s.Port = port })
			save()
		})
	}

	mSens := lbl.add(systray.AddMenuItem(T("sens"), T("sens.tip")), text("sens"), text("sens.tip"))
	var sensItems radio
	for i, lvl := range sensitivity {
		item := lbl.add(mSens.AddSubMenuItemCheckbox(T(lvl.key), "", lvl.gain == s.Gain), text(lvl.key), nil)
		sensItems = append(sensItems, item)
		onClick(item, func() {
			sensItems.set(i)
			eng.Update(func(s *Settings) { s.Gain = lvl.gain })
			save()
		})
	}

	mLang := lbl.add(systray.AddMenuItem(T("lang"), ""), text("lang"), nil)
	var langItems radio
	for i, l := range languages {
		title := func() string { return l.title }
		if l.setting == langAuto {
			title = text("lang.auto")
		}
		item := mLang.AddSubMenuItemCheckbox(title(), "", l.setting == s.Language)
		if l.setting == langAuto {
			lbl.add(item, title, nil)
		}
		langItems = append(langItems, item)
		onClick(item, func() {
			langItems.set(i)
			eng.Update(func(s *Settings) { s.Language = l.setting })
			save()
			setLanguage(l.setting)
			lbl.refresh()
			showStatus()
			showUpdate()
		})
	}
	systray.AddSeparator()

	// Updates: the automatic check can be switched off; "Check now" always answers.
	mUpdates := lbl.add(systray.AddMenuItem(T("updates"), ""), text("updates"), nil)
	mVersion := lbl.add(mUpdates.AddSubMenuItem(fmt.Sprintf(T("upd.version"), version), ""),
		func() string { return fmt.Sprintf(T("upd.version"), version) }, nil)
	mVersion.Disable()
	mUpdAuto := lbl.add(mUpdates.AddSubMenuItemCheckbox(T("upd.auto"), T("upd.auto.tip"), !s.NoUpdateCheck),
		text("upd.auto"), text("upd.auto.tip"))
	onClick(mUpdAuto, func() {
		on := !mUpdAuto.Checked()
		if on {
			mUpdAuto.Check()
		} else {
			mUpdAuto.Uncheck()
		}
		eng.Update(func(s *Settings) { s.NoUpdateCheck = !on })
		save()
	})
	mUpdCheck := lbl.add(mUpdates.AddSubMenuItem(T("upd.check"), ""), text("upd.check"), nil)
	onClick(mUpdCheck, func() {
		r, err := upd.Check(ctx)
		switch {
		case err != nil:
			log.Printf("%v", err)
			notify(T("upd.failed"))
		case r == nil:
			notify(fmt.Sprintf(T("upd.latest"), version))
		default:
			if r.Version() == eng.Settings().SkipVersion { // asking again brings a skipped version back
				eng.Update(func(s *Settings) { s.SkipVersion = "" })
				save()
			}
			showUpdate()
			notify(fmt.Sprintf(T("upd.notify"), r.Version()))
		}
	})

	mLogin := lbl.add(systray.AddMenuItemCheckbox(T("login"), T("login.tip"), autostartEnabled()), text("login"), text("login.tip"))
	onClick(mLogin, func() {
		enable := !mLogin.Checked()
		if err := setAutostart(enable); err != nil {
			log.Printf("autostart: %v", err)
			return
		}
		if enable {
			mLogin.Check()
		} else {
			mLogin.Uncheck()
		}
	})
	mLog := lbl.add(systray.AddMenuItem(T("log"), T("log.tip")), text("log"), text("log.tip"))
	onClick(mLog, func() { _ = openFile(logPath()) })
	mQuit := lbl.add(systray.AddMenuItem(T("quit"), ""), text("quit"), nil)
	onClick(mQuit, func() {
		stop()
		systray.Quit()
	})

	eng.OnStatus = func(st Status) {
		iconMu.Lock()
		isDark := dark
		iconMu.Unlock()
		setIcon(st.State, isDark)
		stMu.Lock()
		status = st
		stMu.Unlock()
		showStatus()
	}
	go eng.Run(ctx)
}
