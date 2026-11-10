package main

import (
	"net"
	"strconv"
	"testing"
)

func TestWorkerReadinessUsesOnlyAdvertisedInterface(t *testing.T) {
	addrs, e := net.InterfaceAddrs()
	if e != nil {
		t.Fatal(e)
	}
	var host string
	for _, a := range addrs {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() {
			host = ip.String()
			break
		}
	}
	if host == "" {
		t.Skip("requires a non-loopback IPv4 interface")
	}
	bind := func() (net.Listener, string) {
		l, e := net.Listen("tcp4", net.JoinHostPort(host, "0"))
		if e != nil {
			t.Fatal(e)
		}
		return l, strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	}
	registry, p1 := bind()
	defer registry.Close()
	engine, p2 := bind()
	defer engine.Close()
	c := config{id: host}
	if !c.rmiPortsReady(p1, p2) {
		t.Fatal("both RMI listeners are available only on the advertised interface")
	}
	if e = engine.Close(); e != nil {
		t.Fatal(e)
	}
	if c.rmiPortsReady(p1, p2) {
		t.Fatal("missing engine listener reported ready")
	}
}
