package app

import (
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/sysintegration"
)

func TestSystemMountURLNormalisesWildcardBind(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ address, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/webdav"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000/webdav"},
		{"", "http://127.0.0.1:8080/webdav"},
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
