package ysync

import (
	"strings"
	"testing"
)

func TestGenerateLaunchAgentPlistEscapesArgumentsAndOmitsSecrets(t *testing.T) {
	cfg := Config{
		LocalRoot:        `/Users/me/Sync & Stuff`,
		RemoteRoot:       `/`,
		BaseURL:          `https://webdav.yandex.ru`,
		ConflictStrategy: `skip`,
		DeleteMode:       `trash`,
		KeychainProfile:  `ysync`,
		User:             `secret-user`,
		Pass:             `secret-pass`,
	}
	plist := GenerateLaunchAgentPlist(cfg, `/usr/local/bin/ysync`)
	for _, want := range []string{
		"<key>Label</key>",
		"<string>com.ysync.service</string>",
		"<string>/Users/me/Sync &amp; Stuff</string>",
		"<string>-keychain-profile</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "secret-user") || strings.Contains(plist, "secret-pass") {
		t.Fatalf("plist leaked credentials:\n%s", plist)
	}
}
