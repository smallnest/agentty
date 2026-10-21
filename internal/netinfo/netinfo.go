// Package netinfo picks the best local IPv4 address to advertise in a pairing
// QR. A laptop often has Wi-Fi, a VPN tunnel, Docker bridges, and AirDrop
// interfaces at once; the first non-loopback address is usually the wrong one.
package netinfo

import (
	"net"
	"sort"
	"strings"
)

// Candidate is one IPv4 address on one interface.
type Candidate struct {
	Name   string
	IP     net.IP
	HasMAC bool
}

// LocalCandidates enumerates usable IPv4 addresses on this host.
func LocalCandidates() []Candidate {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipnet.IP.To4()
			if v4 == nil {
				continue
			}
			out = append(out, Candidate{
				Name:   iface.Name,
				IP:     v4,
				HasMAC: len(iface.HardwareAddr) > 0,
			})
		}
	}
	return out
}

// Best returns the address to advertise, or "" when none qualifies.
func Best() string {
	return BestOf(LocalCandidates(), defaultRouteIPv4())
}

// BestOf ranks candidates and returns the highest-scoring usable address. When
// every candidate is virtual, it falls back to the default-route address.
func BestOf(candidates []Candidate, defaultIP net.IP) string {
	for _, candidate := range Rank(candidates, defaultIP) {
		if score(candidate, defaultIP) >= 0 {
			return candidate.IP.String()
		}
	}
	if defaultIP != nil && !defaultIP.IsLoopback() && !defaultIP.IsUnspecified() {
		return defaultIP.String()
	}
	return ""
}

// All returns usable addresses, best first.
func All() []string {
	ranked := Rank(LocalCandidates(), defaultRouteIPv4())
	out := make([]string, 0, len(ranked))
	for _, candidate := range ranked {
		if score(candidate, nil) < 0 {
			continue
		}
		out = append(out, candidate.IP.String())
	}
	return out
}

// IPv4Of returns the address of a named interface, or "".
func IPv4Of(name string) string {
	for _, candidate := range LocalCandidates() {
		if candidate.Name == name {
			return candidate.IP.String()
		}
	}
	return ""
}

// Rank sorts candidates best-first; equal scores keep interface order.
func Rank(candidates []Candidate, defaultIP net.IP) []Candidate {
	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)
	sort.SliceStable(ranked, func(i, j int) bool {
		return score(ranked[i], defaultIP) > score(ranked[j], defaultIP)
	})
	return ranked
}

var virtualPrefixes = []string{
	"utun", "tun", "tap", "wg", "tailscale", "zt", "docker", "br-", "veth",
	"virbr", "vmnet", "vboxnet", "awdl", "llw", "anpi", "bridge", "gif", "stf",
}

var physicalPrefixes = []string{
	"en", "eth", "wlan", "wlp", "enp", "eno", "ens", "wl", "em",
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func score(candidate Candidate, defaultIP net.IP) int {
	ip := candidate.IP
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return -1000
	}
	if cgnat.Contains(ip) {
		return -500
	}

	name := strings.ToLower(candidate.Name)
	value := 0
	if hasAnyPrefix(name, virtualPrefixes) {
		value -= 200
	}
	if hasAnyPrefix(name, physicalPrefixes) {
		value += 100
	}
	if candidate.HasMAC {
		value += 20
	}
	if ip.IsPrivate() {
		value += 50
	}
	if defaultIP != nil && ip.Equal(defaultIP) {
		value += 10
	}
	return value
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// defaultRouteIPv4 asks the kernel which source address it would use. A UDP
// connect sends no packets; it only resolves the route.
func defaultRouteIPv4() net.IP {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP
	}
	return nil
}
