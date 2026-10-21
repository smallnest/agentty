package netinfo

import (
	"net"
	"testing"
)

func ipv4(value string) net.IP {
	return net.ParseIP(value).To4()
}

func TestBestOfPrefersPhysicalLAN(t *testing.T) {
	candidates := []Candidate{
		{Name: "utun3", IP: ipv4("100.64.0.1"), HasMAC: false},
		{Name: "docker0", IP: ipv4("172.17.0.1"), HasMAC: true},
		{Name: "awdl0", IP: ipv4("169.254.1.1"), HasMAC: true},
		{Name: "en0", IP: ipv4("192.168.1.23"), HasMAC: true},
	}
	if got := BestOf(candidates, nil); got != "192.168.1.23" {
		t.Fatalf("BestOf = %q, want 192.168.1.23", got)
	}
}

func TestBestOfPrefersWiFiOverEthernetTie(t *testing.T) {
	// Both physical; stable sort keeps the first interface when scores tie.
	candidates := []Candidate{
		{Name: "en0", IP: ipv4("192.168.1.23"), HasMAC: true},
		{Name: "en1", IP: ipv4("192.168.1.24"), HasMAC: true},
	}
	if got := BestOf(candidates, nil); got != "192.168.1.23" {
		t.Fatalf("BestOf = %q, want 192.168.1.23", got)
	}
}

func TestBestOfFallsBackToDefaultRoute(t *testing.T) {
	candidates := []Candidate{
		{Name: "docker0", IP: ipv4("172.17.0.1"), HasMAC: true},
	}
	if got := BestOf(candidates, ipv4("10.0.0.5")); got != "10.0.0.5" {
		t.Fatalf("BestOf = %q, want 10.0.0.5", got)
	}
}

func TestBestOfRejectsLinkLocalAndCGNAT(t *testing.T) {
	candidates := []Candidate{
		{Name: "en0", IP: ipv4("169.254.10.10"), HasMAC: true},
		{Name: "utun3", IP: ipv4("100.64.0.1"), HasMAC: false},
	}
	if got := BestOf(candidates, nil); got != "" {
		t.Fatalf("BestOf = %q, want empty", got)
	}
}

func TestRankOrdersVirtualLast(t *testing.T) {
	candidates := []Candidate{
		{Name: "wg0", IP: ipv4("10.9.0.1"), HasMAC: false},
		{Name: "eth0", IP: ipv4("192.168.0.10"), HasMAC: true},
	}
	ranked := Rank(candidates, nil)
	if ranked[0].Name != "eth0" {
		t.Fatalf("first = %q, want eth0", ranked[0].Name)
	}
	if ranked[1].Name != "wg0" {
		t.Fatalf("second = %q, want wg0", ranked[1].Name)
	}
}
