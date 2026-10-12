package main

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestSplitHostPort(t *testing.T) {
	for _, test := range []struct {
		address string
		host    string
		port    string
		wantErr bool
	}{
		{address: "example.org:19132", host: "example.org", port: "19132"},
		{address: "[2001:db8::1]:19132", host: "2001:db8::1", port: "19132"},
		{address: "example.org", wantErr: true},
		{address: ":19132", wantErr: true},
		{address: "example.org:0", wantErr: true},
		{address: "example.org:65536", wantErr: true},
	} {
		host, port, err := splitHostPort(test.address)
		if (err != nil) != test.wantErr {
			t.Errorf("splitHostPort(%q) error = %v, wantErr %t", test.address, err, test.wantErr)
			continue
		}
		if !test.wantErr && (host != test.host || port != test.port) {
			t.Errorf("splitHostPort(%q) = %q, %q; want %q, %q", test.address, host, port, test.host, test.port)
		}
	}
}

func TestRedactPlayerAddress(t *testing.T) {
	playerAddr := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 34567}
	message := redactPlayerAddress(errors.New("read from 192.0.2.1:34567 failed"), playerAddr)
	if strings.Contains(message, playerAddr.String()) || !strings.Contains(message, "[client]") {
		t.Fatalf("player address was not redacted: %q", message)
	}
}
