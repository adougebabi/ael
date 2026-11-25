package main

import (
	"ael/internal/bootstrap"
	"ael/internal/remote"
	"fmt"
	"golang.org/x/mod/semver"
	"os"
	"sync"
)

var updateNoticeOnce sync.Once

func clientVersionNotice(current string, m remote.ServerMetadata) string {
	newer := func(version string) bool {
		return semver.IsValid("v"+version) && semver.Compare("v"+version, "v"+current) > 0
	}
	switch {
	case newer(m.MinimumClientVersion):
		return fmt.Sprintf("CLI %s 低于 Server 要求的最低版本 %s。请执行 upgrade，然后在项目中执行 ./ael update。", current, m.MinimumClientVersion)
	case newer(m.RecommendedClientVersion):
		return fmt.Sprintf("CLI %s，Server 推荐客户端 %s。请执行 upgrade，然后在项目中执行 ./ael update。", current, m.RecommendedClientVersion)
	case m.RecommendedClientVersion == "" && newer(m.Version):
		return fmt.Sprintf("CLI %s，Server %s。可用 upgrade --check 查询已发布客户端；升级后执行 ./ael update。", current, m.Version)
	}
	return ""
}
func notifyClientVersion(meta remote.ServerMetadata) {
	if notice := clientVersionNotice(bootstrap.Version, meta); notice != "" {
		updateNoticeOnce.Do(func() { fmt.Fprintln(os.Stderr, "AEL · "+notice) })
	}
}
