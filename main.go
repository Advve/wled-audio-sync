// WLED Audio Sync: sends whatever this computer is playing to WLED lights.
//
// It captures the system's audio output (no microphone needed), analyses it and broadcasts
// it in WLED AudioReactive's "audio sync" v2 format. Every WLED device on the network with
// AudioReactive sync mode "Receive" reacts to it, all from the same data, so they stay in time.
//
// Runs as a tray icon by default; --no-tray runs in a terminal with a live meter.
package main

// The Windows .exe icon, version info and manifest are embedded from rsrc_windows_*.syso.
// To regenerate them after changing the icon or winres/winres.json:
//go:generate go run . --write-icons winres
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/ipv4"
)

func main() {
	var (
		noTray  = flag.Bool("no-tray", false, "run in the terminal with a live meter instead of the tray")
		list    = flag.Bool("list", false, "list output devices and exit")
		listen  = flag.Bool("listen", false, "debug: show audio sync packets arriving on this computer")
		quiet   = flag.Bool("quiet", false, "with --no-tray: no meter")
		device  = flag.String("device", "", "output device to capture (name contains this); default: current default output")
		port    = flag.Int("port", defaultPort, "WLED AudioReactive sync port (\"channel\")")
		gain    = flag.Float64("gain", 1, "sensitivity multiplier")
		iface   = flag.String("interface", "", "this PC's IP on the network to broadcast on (default: the internet connection's)")
		unicast = flag.String("unicast", "", "comma-separated WLED device IPs to also send to directly")
		multi   = flag.Bool("multicast", false, "also send to WLED's multicast group 239.0.0.1")
		noBcast = flag.Bool("no-broadcast", false, "don't broadcast; only send to --unicast IPs (and --multicast)")
		squelch = flag.Float64("squelch", -60, "silence threshold in dBFS")
		beat    = flag.Float64("beat", 1.5, "beat threshold: bass vs. its recent average")
		fps     = flag.Int("fps", 50, "packets per second")
		lang    = flag.String("lang", "", "tray language: en or pl (default: saved choice, else the system's)")
		showVer = flag.Bool("version", false, "print the version and exit")
		updated = flag.Bool("updated", false, "internal: started by an update; waits for the old copy to exit")

		writeIcons = flag.String("write-icons", "", "developer: write the app icon PNGs to this folder and exit")
	)
	flag.Usage = func() { // --help (and bad flags): show in the terminal, even from the windowless build
		attachConsole()
		fmt.Fprintf(os.Stderr, "WLED Audio Sync %s\n\nUsage: wled-audio-sync [options]\nWithout options it runs in the system tray.\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	// Saved settings (changed from the tray menu), overridden by flags given on the command line.
	s := loadSettings()
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "device":
			s.Device = *device
		case "port":
			s.Port = *port
		case "gain":
			s.Gain = *gain
		case "interface":
			s.Interface = *iface
		case "unicast":
			s.Unicast = splitList(*unicast)
		case "multicast":
			s.Multicast = *multi
		case "no-broadcast":
			s.NoBroadcast = *noBcast
		case "squelch":
			s.SquelchDB = *squelch
		case "beat":
			s.Beat = *beat
		case "fps":
			s.FPS = *fps
		case "lang":
			s.Language = *lang
		}
	})

	switch {
	case *showVer:
		attachConsole()
		fmt.Println("WLED Audio Sync", version)
	case *list:
		attachConsole()
		listDevices()
	case *listen:
		attachConsole()
		listenPackets(s.Port, sendFromIP(s))
	case *noTray:
		attachConsole()
		setLanguage(s.Language)
		release, ok := acquireSingleInstance(instanceName)
		if !ok {
			fmt.Println(T("already"))
			return
		}
		defer release()
		runConsole(s, *quiet)
	case *writeIcons != "":
		if err := writeAppIcons(*writeIcons); err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
	default:
		// Check before touching the log file: a second copy must not wipe the running one's log.
		setLanguage(s.Language)
		release, ok := acquireSingleInstance(instanceName)
		for i := 0; !ok && *updated && i < 40; i++ { // the copy that installed the update is exiting
			time.Sleep(250 * time.Millisecond)
			release, ok = acquireSingleInstance(instanceName)
		}
		if !ok {
			showAlreadyRunning()
			return
		}
		defer release()
		setupLogFile()
		log.Printf("WLED Audio Sync %s starting, settings from %s", version, settingsPath())
		go removeOldExe()
		repairAutostart()
		runTray(NewEngine(s), *updated)
	}
}

// instanceName identifies the running sender (tray or --no-tray); only one may run per user, as two
// senders would make the lights jump between their streams. --list / --listen don't send, so they may
// run alongside.
const instanceName = "WLED-Audio-Sync"

// repairAutostart re-points "Start at login" at this copy if the program it pointed to was moved
// or deleted. A working entry is left alone, so trying a second copy doesn't take it over.
func repairAutostart() {
	p := autostartPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		if err := setAutostart(true); err != nil {
			log.Printf("autostart: %v", err)
		} else {
			log.Printf("autostart pointed to %s, which no longer exists; now points to this copy", p)
		}
	}
}

// writeAppIcons writes the app icon as PNGs for embedding in the Windows .exe (see winres/).
func writeAppIcons(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, size := range []int{16, 20, 24, 32, 40, 48, 64, 128, 256} {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("icon_%d.png", size)))
		if err != nil {
			return err
		}
		err = png.Encode(f, renderIcon(size, StateActive, true))
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func setupLogFile() {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return
	}
	if f, err := os.Create(logPath()); err == nil {
		log.SetOutput(f)
	}
}

func listDevices() {
	names, err := listOutputDevices()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Output devices (use with --device \"<part of the name>\"):")
	for _, n := range names {
		fmt.Println("  " + n)
	}
}

// runConsole runs without the tray, printing a live meter until Ctrl+C.
func runConsole(s Settings, quiet bool) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	eng := NewEngine(s)
	var lastPrint time.Time
	var status Status
	eng.OnStatus = func(st Status) {
		if st.Device != status.Device || st.Target != status.Target || (st.Err != status.Err && st.Err != "") {
			fmt.Printf("\r%-79s\n", fmt.Sprintf("%s → %s %s", st.Device, st.Target, st.Err))
		}
		status = st
	}
	if !quiet {
		var beatUntil time.Time
		eng.OnFrame = func(f Frame, state State) {
			if f.Beat {
				beatUntil = time.Now().Add(150 * time.Millisecond) // a beat is one frame; keep it visible
			}
			if time.Since(lastPrint) < 100*time.Millisecond {
				return
			}
			lastPrint = time.Now()
			f.Beat = time.Now().Before(beatUntil)
			note := ""
			if state != StateActive {
				note = status.Text()
			}
			fmt.Print(meter(f, note))
		}
	}
	fmt.Println("Sending this computer's audio to WLED. Ctrl+C to stop.")
	eng.Run(ctx)
	fmt.Println("\nStopped.")
}

const bars = " ▁▂▃▄▅▆▇█"

func meter(f Frame, note string) string {
	vol := strings.Repeat("#", int(f.VolumeSmooth/255*12))
	var eq strings.Builder
	levels := []rune(bars)
	for _, v := range f.Bands {
		eq.WriteRune(levels[min(len(levels)-1, int(v/255*float32(len(levels))))])
	}
	beat := "    "
	if f.Beat {
		beat = "BEAT"
	}
	line := fmt.Sprintf(" vol [%-12s] eq [%s] %s %5.0f Hz %s", vol, eq.String(), beat, f.MajorPeak, note)
	if r := []rune(line); len(r) > 79 {
		line = string(r[:79])
	}
	return fmt.Sprintf("\r%-79s", line)
}

// listenPackets shows audio sync packets arriving on this computer (debug): broadcast, unicast
// and multicast alike.
func listenPackets(port int, ifaceIP string) {
	if ifaceIP == "" {
		ifaceIP = guessLANIP()
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer conn.Close()
	ifi, _ := interfaceByIP(ifaceIP)
	_ = ipv4.NewPacketConn(conn).JoinGroup(ifi, &net.UDPAddr{IP: net.ParseIP(multicastGroup)})
	fmt.Printf("Listening for WLED audio sync on port %d (Ctrl+C to stop)\n", port)
	buf := make([]byte, 128)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				fmt.Printf("\r%-79s", " (no packets)")
				continue
			}
			if err != io.EOF {
				fmt.Println("\nError:", err)
			}
			return
		}
		if f, ok := decodePacket(buf[:n]); ok {
			fmt.Print(meter(f, "from "+from.IP.String()))
		} else {
			fmt.Printf("\r%-79s", fmt.Sprintf(" unknown packet (%d bytes) from %s", n, from.IP))
		}
	}
}
