package app

import (
	"net"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/sysintegration"
)

func TestSystemMountURLNormalisesWildcardBind(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ address, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/webdav"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000/webdav"},
		{"", "http://127.0.0.1:18787/webdav"},
	} {
		handler := &systemHandler{httpAddress: test.address}
		if got := handler.mountURL(); got != test.want {
			t.Errorf("mountURL(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestFirstBrokenItemNamesTheActionableProblem(t *testing.T) {
	t.Parallel()
	prerequisites := sysintegration.Prerequisites{Items: []sysintegration.PrerequisiteItem{
		{Key: "ok", OK: true, Description: "fine"},
		{Key: "basic_auth_level", OK: false, Description: "needs BasicAuthLevel=2"},
	}}
	if got := firstBrokenItem(prerequisites); got != "needs BasicAuthLevel=2" {
		t.Fatalf("firstBrokenItem = %q", got)
	}
	if got := firstBrokenItem(sysintegration.Prerequisites{}); got != "" {
		t.Fatalf("firstBrokenItem(empty) = %q", got)
	}
}

func TestSystemSiteURLKeepsThePort(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ address, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000/"},
		{"[::]:9000", "http://127.0.0.1:9000/"},
		{"192.168.1.5:18787", "http://192.168.1.5:18787/"},
		// A missing port is not a reason to hand back a URL that goes nowhere.
		{"192.168.1.5", "http://192.168.1.5:18787/"},
	} {
		if got := siteURL(test.address); got != test.want {
			t.Errorf("siteURL(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestSystemNetworkURLPrefersTheRealInterface(t *testing.T) {
	t.Parallel()
	lan := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("fe80::1"), net.ParseIP("192.168.1.5")}
	for _, test := range []struct {
		name    string
		address string
		addrs   []net.IP
		want    string
	}{
		{"wildcard bind uses the interface", "0.0.0.0:18787", lan, "http://192.168.1.5:18787/"},
		{"loopback bind uses the interface", "127.0.0.1:18787", lan, "http://192.168.1.5:18787/"},
		{"named bind keeps its address", "10.0.0.7:9000", lan, "http://10.0.0.7:9000/"},
		{"no usable interface", "127.0.0.1:18787", []net.IP{net.ParseIP("::1")}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := networkURLFor(test.address, test.addrs); got != test.want {
				t.Fatalf("networkURLFor(%q, %v) = %q, want %q", test.address, test.addrs, got, test.want)
			}
		})
	}
}
