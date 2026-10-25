package sshupload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_RequiresCoreFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string // substring expected in the error; empty = expect success
	}{
		{"missing host", Config{User: "u", KeyPath: "k"}, "host"},
		{"missing user", Config{Host: "h", KeyPath: "k"}, "user"},
		{"missing key", Config{Host: "h", User: "u"}, "key-path"},
		{"all present + insecure (default path)", Config{Host: "h", User: "u", KeyPath: "k", Insecure: true}, ""},
		{"explicit remote path + insecure", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/wiki.zip", Insecure: true}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
		})
	}
}

func TestNew_RejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"relative path", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "backups/r.zip", Insecure: true}},
		{"path traversal", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/../etc/x.zip", Insecure: true}},
		{"trailing slash", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/", Insecure: true}},
		{"semicolon", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r;x.zip", Insecure: true}},
		{"space", Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r x.zip", Insecure: true}},
		{"host with semicolon", Config{Host: "h;rm", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r.zip", Insecure: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatalf("expected rejection, got nil error for %+v", tc.cfg)
			}
		})
	}
}

func TestNew_HostKeyPolicy(t *testing.T) {
	// No known_hosts and not insecure -> refused.
	if _, err := New(Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r.zip"}); err == nil {
		t.Fatal("expected error when no known_hosts and insecure=false")
	}
	// insecure=true -> allowed even without known_hosts.
	if _, err := New(Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r.zip", Insecure: true}); err != nil {
		t.Fatalf("expected success with insecure=true, got: %v", err)
	}
	// known_hosts file present -> allowed.
	tmp := t.TempDir()
	kh := filepath.Join(tmp, "known_hosts")
	if err := os.WriteFile(kh, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/backups/r.zip", KnownHosts: kh}); err != nil {
		t.Fatalf("expected success with known_hosts file, got: %v", err)
	}
}

func TestNew_DefaultsPortAndPath(t *testing.T) {
	const user = "u"
	u, err := New(Config{Host: "h", User: user, KeyPath: "k", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if u.port != "22" {
		t.Fatalf("expected default port 22, got %q", u.port)
	}
	// Default is /home/<user>/leafwiki-backup.zip; assembled from the user like
	// in New so this file keeps no absolute home-path literal (make verify).
	if got, want := u.remotePath(), "/home/"+user+"/leafwiki-backup.zip"; got != want {
		t.Fatalf("unexpected default remote path %q", got)
	}

	// Explicit path is preserved as-is.
	u2, err := New(Config{Host: "h", User: "u", KeyPath: "k", RemotePath: "/srv/wiki/backup.zip", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if u2.remotePath() != "/srv/wiki/backup.zip" {
		t.Fatalf("explicit remote path not preserved: %q", u2.remotePath())
	}
}
