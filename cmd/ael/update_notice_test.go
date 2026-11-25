package main

import (
	"ael/internal/remote"
	"strings"
	"testing"
)

func TestClientVersionNotice(t *testing.T) {
	for _, test := range []struct {
		meta remote.ServerMetadata
		want string
	}{
		{remote.ServerMetadata{Version: "0.2.0"}, "upgrade --check"},
		{remote.ServerMetadata{RecommendedClientVersion: "0.2.0"}, "推荐客户端"},
		{remote.ServerMetadata{MinimumClientVersion: "0.2.0"}, "最低版本"},
		{remote.ServerMetadata{Version: "0.1.0"}, ""},
	} {
		notice := clientVersionNotice("0.1.1", test.meta)
		if test.want == "" && notice != "" || !strings.Contains(notice, test.want) {
			t.Errorf("unexpected notice %q", notice)
		}
	}
}
