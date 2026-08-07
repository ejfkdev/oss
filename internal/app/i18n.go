package app

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Help text i18n: Chinese is shown when the system language is Chinese,
// English otherwise. Detection order (same as ejfkdev/dns):
//  1. OSS_LANG env var (explicit override: zh / en)
//  2. LC_ALL / LC_MESSAGES / LANG (zh* -> Chinese; any other explicit
//     language -> English)
//  3. macOS system preference (defaults read -g AppleLanguages)

var chineseEnv = detectChinese()

func detectChinese() bool {
	// Explicit override for the tool itself.
	switch strings.ToLower(os.Getenv("OSS_LANG")) {
	case "zh", "zh_cn", "zh-cn", "chinese":
		return true
	case "en", "en_us", "en-us", "english":
		return false
	}
	// Standard locale env vars take precedence over system preferences.
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.ToLower(os.Getenv(k))
		if v == "" {
			continue
		}
		if strings.Contains(v, "zh") || strings.Contains(v, "chinese") {
			return true
		}
		// An explicit non-Chinese locale (en_US, ja_JP, ...) wins.
		if len(v) >= 2 && !strings.HasPrefix(v, "c") && !strings.HasPrefix(v, "posix") {
			return false
		}
	}
	// macOS: fall back to the system language preference.
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			if strings.Contains(string(out), "zh") {
				return true
			}
		}
	}
	return false
}

// T returns the Chinese text when the system language is Chinese, otherwise
// the English one.
func T(zh, en string) string {
	if chineseEnv {
		return zh
	}
	return en
}
