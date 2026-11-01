package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/net/ipv4"
)

// Sender sends packets on the local network only. By default it broadcasts to the LAN's subnet:
// many routers don't pass multicast from wired PCs to WiFi devices, but they do pass broadcast
// (WLED's own device-to-device sync uses it too). Multicast and direct unicast are optional.
type Sender struct {
	conn    *net.UDPConn
	targets []*net.UDPAddr
	LANIP   string // interface the targets were worked out for
	Desc    string // e.g. "192.168.1.255:11988"
}

type Delivery struct {
	Broadcast bool
	Multicast bool
	Unicast   []string
}

func NewSender(port int, ifaceIP string, d Delivery) (*Sender, error) {
	lc := net.ListenConfig{Control: enableBroadcast}
	pconn, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	conn := pconn.(*net.UDPConn)
	if ifaceIP == "" {
		ifaceIP = guessLANIP()
	}
	s := &Sender{conn: conn, LANIP: ifaceIP}
	ifi, ipnet := interfaceByIP(ifaceIP)

	if d.Broadcast {
		bcast := net.IPv4bcast
		if ipnet != nil {
			bcast = subnetBroadcast(ipnet)
		}
		s.targets = append(s.targets, &net.UDPAddr{IP: bcast, Port: port})
	}
	if d.Multicast {
		pc := ipv4.NewPacketConn(conn)
		_ = pc.SetMulticastTTL(1)
		if ifi != nil { // send out of the real LAN, not a VPN / WSL / Hyper-V adapter
			_ = pc.SetMulticastInterface(ifi)
		}
		s.targets = append(s.targets, &net.UDPAddr{IP: net.ParseIP(multicastGroup), Port: port})
	}
	for _, ip := range d.Unicast {
		addr := net.ParseIP(strings.TrimSpace(ip))
		if addr == nil {
			conn.Close()
			return nil, fmt.Errorf("invalid IP %q", ip)
		}
		s.targets = append(s.targets, &net.UDPAddr{IP: addr, Port: port})
	}
	if len(s.targets) == 0 {
		conn.Close()
		return nil, fmt.Errorf("nowhere to send: broadcast is off and no unicast IPs are set")
	}

	var names []string
	for _, t := range s.targets {
		names = append(names, t.String())
	}
	s.Desc = strings.Join(names, ", ") // e.g. "192.168.1.255:11988" (shown in the tray menu)
	return s, nil
}

func (s *Sender) Send(b []byte) error {
	var lastErr error
	for _, t := range s.targets {
		if _, err := s.conn.WriteToUDP(b, t); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (s *Sender) Close() { s.conn.Close() }

// guessLANIP returns the IP of the interface used for the default route (nothing is sent).
func guessLANIP() string {
	c, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func interfaceByIP(ip string) (*net.Interface, *net.IPNet) {
	want := net.ParseIP(ip)
	if want == nil {
		return nil, nil
	}
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		addrs, _ := ifaces[i].Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(want) {
				return &ifaces[i], n
			}
		}
	}
	return nil, nil
}

// subnetBroadcast returns e.g. 192.168.1.255 for 192.168.1.23/24.
func subnetBroadcast(n *net.IPNet) net.IP {
	ip, mask := n.IP.To4(), n.Mask
	if len(mask) == net.IPv6len {
		mask = mask[12:]
	}
	if ip == nil || len(mask) != net.IPv4len {
		return net.IPv4bcast
	}
	b := make(net.IP, 4)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}

func joinPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
