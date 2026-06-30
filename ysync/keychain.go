package ysync

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type keychainCredential struct {
	User string
	Pass string
}

var lookupKeychainCredentials = readKeychainCredentials

func ResolveCredentials(cfg *Config) error {
	if cfg.KeychainProfile != "" {
		creds, err := lookupKeychainCredentials(cfg.KeychainProfile)
		if err != nil {
			return err
		}
		cfg.User = creds.User
		cfg.Pass = creds.Pass
	} else if cfg.User == "" && cfg.Pass == "" && cfg.Token == "" {
		if creds, err := lookupKeychainCredentials("ysync"); err == nil && (creds.User != "" || creds.Pass != "") {
			cfg.User = creds.User
			cfg.Pass = creds.Pass
		}
	}

	if cfg.User == "" {
		cfg.User = os.Getenv("YANDEX_USER")
	}
	if cfg.Pass == "" {
		cfg.Pass = os.Getenv("YANDEX_PASS")
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("YANDEX_TOKEN")
	}
	if cfg.Token != "" && cfg.Pass == "" {
		cfg.Pass = cfg.Token
	}
	return nil
}

func readKeychainCredentials(profile string) (keychainCredential, error) {
	if runtime.GOOS != "darwin" {
		return keychainCredential{}, errors.New("keychain credentials are only available on macOS")
	}
	if profile == "" {
		profile = "ysync"
	}
	user, err := readKeychainPassword(profile, "yandex_user")
	if err != nil {
		return keychainCredential{}, err
	}
	pass, err := readKeychainPassword(profile, "yandex_pass")
	if err != nil {
		return keychainCredential{}, err
	}
	return keychainCredential{User: user, Pass: pass}, nil
}

func readKeychainPassword(service, account string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}
