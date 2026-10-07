// Package sshupload uploads a LeafWiki snapshot ZIP to a remote host over SSH
// using the SCP protocol, implemented in pure Go on top of golang.org/x/crypto/ssh
// (no external `scp` binary is invoked on either side beyond the remote's standard
// `scp -t` sink). Authentication is private-key only; the matching public key must
// already be installed in the remote user's ~/.ssh/authorized_keys.
package sshupload

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Config holds the SSH upload settings. It is populated from the
// LEAFWIKI_SNAPSHOT_SSH_* environment variables / --snapshot-ssh-* flags.
type Config struct {
	Host          string // remote host (hostname or IP)
	Port          string // SSH port, defaults to "22" when empty
	User          string // remote SSH user
	KeyPath       string // path to the private key file (never the key contents)
	KeyPassphrase string // optional passphrase for an encrypted private key
	KnownHosts    string // path to a known_hosts file for host key verification
	Insecure      bool   // skip host key verification (INSECURE)
	RemotePath    string // absolute destination path on the remote (default /home/<user>/leafwiki-backup.zip)
}

// safeChars matches values that are safe to pass as an scp -t argument without
// shell interpretation. Restricting this rejects shell metacharacters and thus
// prevents command injection through the host/user fields.
var safeChars = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)

// safePath matches a remote file path that is safe to pass as an scp -t argument:
// it allows slashes (so a full absolute path works) but rejects shell
// metacharacters, preventing command injection through the remote-path field.
var safePath = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// New validates the configuration and returns an Uploader, or an error if the
// configuration is incomplete or unsafe. A nil error means the uploader is ready.
// Callers should treat an error as "feature disabled" and keep running.
func New(cfg Config) (*Uploader, error) {
	if cfg.Host == "" || cfg.User == "" || cfg.KeyPath == "" {
		return nil, fmt.Errorf("snapshot SSH upload requires host, user and key-path; leaving it disabled")
	}
	if !safeChars.MatchString(cfg.Host) {
		return nil, fmt.Errorf("snapshot SSH host contains unsupported characters")
	}
	if !safeChars.MatchString(cfg.User) {
		return nil, fmt.Errorf("snapshot SSH user contains unsupported characters")
	}
	// Default the remote path to /home/<user>/leafwiki-backup.zip when unset.
	remotePath := cfg.RemotePath
	if remotePath == "" {
		remotePath = "/home/" + cfg.User + "/leafwiki-backup.zip"
	}
	if !safePath.MatchString(remotePath) {
		return nil, fmt.Errorf("snapshot SSH remote-path contains unsupported characters")
	}
	if !strings.HasPrefix(remotePath, "/") {
		return nil, fmt.Errorf("snapshot SSH remote-path must be an absolute path (start with /)")
	}
	if strings.Contains(remotePath, "..") {
		return nil, fmt.Errorf("snapshot SSH remote-path must not contain '..'")
	}
	if strings.HasSuffix(remotePath, "/") {
		return nil, fmt.Errorf("snapshot SSH remote-path must not end with a slash")
	}
	port := cfg.Port
	if port == "" {
		port = "22"
	}
	// Validate the host key policy up front so a misconfigured known_hosts file
	// fails fast instead of on the first upload.
	if _, err := hostKeyCallback(cfg); err != nil {
		return nil, err
	}
	return &Uploader{
		host:       cfg.Host,
		port:       port,
		user:       cfg.User,
		keyPath:    cfg.KeyPath,
		keyPass:    cfg.KeyPassphrase,
		knownHosts: cfg.KnownHosts,
		insecure:   cfg.Insecure,
		path:       remotePath,
	}, nil
}

// Uploader performs SCP uploads of snapshot ZIPs to a configurable absolute
// remote path. Each upload overwrites the previous file at that path.
type Uploader struct {
	host       string
	port       string
	user       string
	keyPath    string
	keyPass    string
	knownHosts string
	insecure   bool
	path       string
}

// remotePath is the absolute destination on the remote host.
func (u *Uploader) remotePath() string {
	return u.path
}

// Upload copies the local file at localPath to the remote over SCP. It blocks
// until the transfer completes. Callers are responsible for invoking it in a
// non-blocking manner if they don't want to delay snapshot creation.
func (u *Uploader) Upload(localPath string) error {
	keyBytes, err := os.ReadFile(u.keyPath)
	if err != nil {
		return fmt.Errorf("read private key %q: %w", u.keyPath, err)
	}
	var signer ssh.Signer
	if u.keyPass != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(u.keyPass))
	} else {
		signer, err = ssh.ParsePrivateKey(keyBytes)
	}
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	hostKeyCb, err := hostKeyCallback(Config{KnownHosts: u.knownHosts, Insecure: u.insecure})
	if err != nil {
		return err
	}

	clientCfg := &ssh.ClientConfig{
		User:            u.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCb,
		Timeout:         30 * time.Second,
	}

	addr := net.JoinHostPort(u.host, u.port)
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	// The remote runs its `scp` binary in sink mode (`-t`). The path is sent as
	// a single quoted argument; the safeChars check above guarantees it cannot
	// carry shell metacharacters.
	cmd := "scp -t " + shellQuote(u.remotePath())
	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("scp start: %w", err)
	}

	ack := func() error {
		buf := make([]byte, 1)
		if _, err := io.ReadFull(stdout, buf); err != nil {
			return fmt.Errorf("read scp ack: %w", err)
		}
		if buf[0] != 0 {
			msg, _ := io.ReadAll(stdout)
			return fmt.Errorf("scp remote error: %s", strings.TrimSpace(string(msg)))
		}
		return nil
	}

	// Initial protocol handshake ack.
	if err := ack(); err != nil {
		return err
	}

	fi, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat local file: %w", err)
	}

	// C<mode> <size> <filename>\n — filename is the basename of the remote path;
	// the remote writes to the full path already given on the `scp -t` command line.
	header := fmt.Sprintf("C0644 %d %s\n", fi.Size(), filepath.Base(u.remotePath()))
	if _, err := stdin.Write([]byte(header)); err != nil {
		return fmt.Errorf("write scp header: %w", err)
	}
	if err := ack(); err != nil {
		return err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(stdin, f); err != nil {
		return fmt.Errorf("copy file body: %w", err)
	}
	// Null byte signals end of file.
	if _, err := stdin.Write([]byte{0}); err != nil {
		return fmt.Errorf("write scp eof: %w", err)
	}
	if err := ack(); err != nil {
		return err
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	}
	if err := session.Wait(); err != nil {
		return fmt.Errorf("scp wait: %w", err)
	}
	return nil
}

// hostKeyCallback builds the SSH host key verification policy:
//   - a configured known_hosts file wins (MITM-protected),
//   - otherwise --insecure (InsecureIgnoreHostKey) is allowed but warned about,
//   - otherwise (no known_hosts, not insecure) we refuse: the caller should
//     disable the feature rather than upload to an unverified host.
func hostKeyCallback(cfg Config) (ssh.HostKeyCallback, error) {
	if cfg.KnownHosts != "" {
		cb, err := knownhosts.New(cfg.KnownHosts)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts %q: %w", cfg.KnownHosts, err)
		}
		return cb, nil
	}
	if cfg.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	return nil, fmt.Errorf("no known_hosts configured and insecure=false; set LEAFWIKI_SNAPSHOT_SSH_KNOWN_HOSTS or LEAFWIKI_SNAPSHOT_SSH_INSECURE=true")
}

// shellQuote wraps a value in single quotes, escaping any embedded single quote.
// Values are already constrained by safeChars, so this is defense in depth.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
