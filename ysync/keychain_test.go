package ysync

import (
	"errors"
	"os"
	"testing"
)

func TestResolveCredentialsUsesExplicitKeychainProfile(t *testing.T) {
	old := lookupKeychainCredentials
	defer func() { lookupKeychainCredentials = old }()
	lookupKeychainCredentials = func(profile string) (keychainCredential, error) {
		if profile != "work" {
			t.Fatalf("profile = %q, want work", profile)
		}
		return keychainCredential{User: "key-user", Pass: "key-pass"}, nil
	}
	cfg := Config{KeychainProfile: "work", User: "cli-user", Pass: "cli-pass"}
	if err := ResolveCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.User != "key-user" || cfg.Pass != "key-pass" {
		t.Fatalf("credentials = %q/%q", cfg.User, cfg.Pass)
	}
}

func TestResolveCredentialsFallsBackToEnvironment(t *testing.T) {
	old := lookupKeychainCredentials
	defer func() { lookupKeychainCredentials = old }()
	lookupKeychainCredentials = func(string) (keychainCredential, error) {
		return keychainCredential{}, errors.New("missing")
	}
	t.Setenv("YANDEX_USER", "env-user")
	t.Setenv("YANDEX_PASS", "env-pass")
	cfg := Config{}
	if err := ResolveCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.User != "env-user" || cfg.Pass != "env-pass" {
		t.Fatalf("credentials = %q/%q", cfg.User, cfg.Pass)
	}
}

func TestResolveCredentialsUsesTokenAsBasicAuthPassword(t *testing.T) {
	old := lookupKeychainCredentials
	defer func() { lookupKeychainCredentials = old }()
	lookupKeychainCredentials = func(string) (keychainCredential, error) {
		return keychainCredential{}, errors.New("missing")
	}
	t.Setenv("YANDEX_TOKEN", "token")
	cfg := Config{User: "cli-user"}
	if err := ResolveCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Pass != "token" {
		t.Fatalf("pass = %q, want token", cfg.Pass)
	}
}

func TestExplicitKeychainErrorIsReturned(t *testing.T) {
	old := lookupKeychainCredentials
	defer func() { lookupKeychainCredentials = old }()
	lookupKeychainCredentials = func(string) (keychainCredential, error) {
		return keychainCredential{}, os.ErrNotExist
	}
	cfg := Config{KeychainProfile: "missing"}
	if err := ResolveCredentials(&cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}
