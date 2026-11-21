// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package restic wraps the restic binary. Calling the binary instead of
// linking the library keeps behaviour identical to restic's own documentation
// and keeps upgrades independent from this project.
package restic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mnnoee/jealous-vault/internal/executil"
)

// Options configures a Client.
type Options struct {
	Binary       string
	Repository   string
	PasswordFile string
	Env          map[string]string
	// CacheDir is passed to restic as RESTIC_CACHE_DIR. Set it when the
	// environment has no HOME or XDG_CACHE_HOME, for example under systemd.
	CacheDir string
	Extra    []string
	Logger   *slog.Logger
	Output   io.Writer
	// ShowSensitive prints raw restic stderr in debug logs. Errors and
	// notifications always keep the redacted form.
	ShowSensitive bool
}

// LockRetry makes exclusive operations wait for another host instead of
// failing immediately. The surrounding context still bounds the total wait.
const LockRetry = "30m"

// MinRestoreVersion is the oldest restic series accepted for restore, drill
// and test-restore.
const MinRestoreVersion = "0.18.0"

// CheckRestoreVersion refuses an unsupported restic series before a restore.
func (c *Client) CheckRestoreVersion(ctx context.Context) error {
	out, err := c.Version(ctx)
	if err != nil {
		return err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return fmt.Errorf("cannot parse restic version %q", strings.TrimSpace(out))
	}
	version := strings.TrimPrefix(fields[1], "v")
	if !versionAtLeast(version, MinRestoreVersion) {
		return fmt.Errorf("restic %s is too old for restore; need >= %s", version, MinRestoreVersion)
	}
	return nil
}

func versionAtLeast(version, minimum string) bool {
	parse := func(value string) [3]int {
		var out [3]int
		for i, part := range strings.SplitN(value, ".", 3) {
			if i >= 3 {
				break
			}
			n, _ := strconv.Atoi(strings.TrimFunc(part, func(r rune) bool {
				return r < '0' || r > '9'
			}))
			out[i] = n
		}
		return out
	}
	a, b := parse(version), parse(minimum)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

// Client runs restic with a fixed repository and environment.
type Client struct {
	binary        string
	repository    string
	passwordFile  string
	cacheDir      string
	env           map[string]string
	extra         []string
	showSensitive bool
	log           *slog.Logger
	out           io.Writer
}

// New returns a Client. Missing optional values fall back to safe defaults.
func New(o Options) *Client {
	binary := o.Binary
	if binary == "" {
		binary = "restic"
	}
	if !strings.Contains(binary, "/") {
		if path, err := executil.LookPath(binary); err == nil {
			binary = path
		}
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	out := o.Output
	if out == nil {
		out = io.Discard
	}
	return &Client{
		binary:        binary,
		repository:    o.Repository,
		passwordFile:  o.PasswordFile,
		cacheDir:      o.CacheDir,
		env:           o.Env,
		extra:         append([]string(nil), o.Extra...),
		showSensitive: o.ShowSensitive,
		log:           log,
		out:           out,
	}
}

// EnvironmentEntries returns the restic-specific variables the agent adds,
// sorted by name. It does not include the process environment.
func (c *Client) EnvironmentEntries() []string {
	env := []string{"RESTIC_REPOSITORY=" + c.repository}
	if c.passwordFile != "" {
		env = append(env, "RESTIC_PASSWORD_FILE="+c.passwordFile)
	}
	if c.cacheDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+c.cacheDir)
	}
	keys := make([]string, 0, len(c.env))
	for k := range c.env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+c.env[k])
	}
	return env
}

func (c *Client) environment() []string {
	return append(executil.SanitizeEnv(os.Environ()), c.EnvironmentEntries()...)
}

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	all := make([]string, 0, len(c.extra)+len(args))
	all = append(all, c.extra...)
	all = append(all, args...)
	cmd := executil.Command(ctx, c.binary, all...)
	// exec.CommandContext kills with SIGKILL by default, which leaves restic
	// locks behind. Ask restic to shut down cleanly first.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = c.environment()
	return cmd
}

// BackupOptions describes one restic backup run.
type BackupOptions struct {
	Host          string
	Paths         []string
	Excludes      []string
	Tags          []string
	OneFileSystem bool
	DryRun        bool
	LimitUpload   int
	Compression   string
	PackSize      int
	// AttemptTimeout bounds one restic run. Zero disables the bound and keeps
	// the overall command deadline only.
	AttemptTimeout time.Duration
}

// Summary is the final restic backup message.
type Summary struct {
	SnapshotID          string  `json:"snapshot_id"`
	FilesNew            int     `json:"files_new"`
	FilesChanged        int     `json:"files_changed"`
	FilesUnmodified     int     `json:"files_unmodified"`
	DirsNew             int     `json:"dirs_new"`
	DataAdded           int64   `json:"data_added"`
	TotalBytesProcessed int64   `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
}

// BackupArgs returns the restic backup arguments for a manual command. It is
// the same builder Backup uses, without --json.
func BackupArgs(o BackupOptions) []string {
	return backupArgs(o, false)
}

func backupArgs(o BackupOptions, jsonOutput bool) []string {
	args := []string{"backup"}
	if jsonOutput {
		args = append(args, "--json")
	}
	if o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	if o.OneFileSystem {
		args = append(args, "--one-file-system")
	}
	if o.DryRun {
		args = append(args, "--dry-run")
	}
	if o.LimitUpload > 0 {
		args = append(args, "--limit-upload", strconv.Itoa(o.LimitUpload))
	}
	if o.Compression != "" && o.Compression != "auto" {
		args = append(args, "--compression", o.Compression)
	}
	if o.PackSize > 0 {
		args = append(args, "--pack-size", strconv.Itoa(o.PackSize))
	}
	for _, tag := range o.Tags {
		args = append(args, "--tag", tag)
	}
	for _, pattern := range o.Excludes {
		args = append(args, "--exclude", pattern)
	}
	return append(args, o.Paths...)
}

// Backup copies paths into the repository and returns restic's summary.
func (c *Client) Backup(ctx context.Context, o BackupOptions) (Summary, error) {
	var summary Summary
	if o.AttemptTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.AttemptTimeout)
		defer cancel()
	}

	args := backupArgs(o, true)
	cmd := c.command(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return summary, err
	}
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return summary, fmt.Errorf("start %s: %w", c.binary, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	found := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"message_type"`)) {
			continue
		}
		var msg struct {
			MessageType string `json:"message_type"`
			Summary
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			c.log.Debug("skip unparsable restic message", "error", err)
			continue
		}
		switch msg.MessageType {
		case "summary":
			summary = msg.Summary
			found = true
		case "error":
			if text := stderr.String(); text != "" {
				c.log.Warn("restic reported an error", "detail", strings.TrimSpace(text))
			}
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if waitErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return summary, fmt.Errorf("restic backup: %w", ctxErr)
		}
		return summary, c.commandError("backup", waitErr, stderr.String())
	}
	if scanErr != nil {
		return summary, fmt.Errorf("read restic output: %w", scanErr)
	}
	if !found {
		return summary, errors.New("restic backup finished without a summary message")
	}
	return summary, nil
}

// Snapshot is one restic snapshot.
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
}

// Snapshots lists snapshots, optionally filtered by host.
func (c *Client) Snapshots(ctx context.Context, host string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json"}
	if host != "" {
		args = append(args, "--host", host)
	}

	var stdout bytes.Buffer
	cmd := c.command(ctx, args...)
	cmd.Stdout = &stdout
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("restic snapshots: %w", ctxErr)
		}
		return nil, c.commandError("snapshots", err, stderr.String())
	}

	data := bytes.TrimSpace(stdout.Bytes())
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, nil
	}
	var snapshots []Snapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil, fmt.Errorf("parse restic snapshots: %w", err)
	}
	return snapshots, nil
}

// RestoreOptions describes one restore.
type RestoreOptions struct {
	Snapshot  string
	Target    string
	Host      string
	Includes  []string
	Overwrite string
	Verify    bool
	// RetryLock makes restic wait for a repository lock held by another host.
	// Empty keeps the default restic behaviour (fail immediately).
	RetryLock string
}

// Restore writes a snapshot into Target. Snapshot may be "latest".
func (c *Client) Restore(ctx context.Context, o RestoreOptions) error {
	if o.Snapshot == "" {
		o.Snapshot = "latest"
	}
	if o.Target == "" {
		o.Target = "/"
	}

	args := []string{"restore", o.Snapshot, "--target", o.Target}
	if o.Snapshot == "latest" && o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	for _, p := range o.Includes {
		args = append(args, "--include", p)
	}
	if o.Overwrite != "" {
		args = append(args, "--overwrite", o.Overwrite)
	}
	if o.Verify {
		args = append(args, "--verify")
	}
	if o.RetryLock != "" {
		args = append(args, "--retry-lock", o.RetryLock)
	}

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic restore: %w", ctxErr)
		}
		return c.commandError("restore", err, stderr.String())
	}
	return nil
}

// Check verifies the repository. readDataSubset is passed to restic unchanged,
// for example "5%" or "1/10".
func (c *Client) Check(ctx context.Context, readDataSubset string) error {
	args := []string{"check"}
	if readDataSubset != "" {
		args = append(args, "--read-data-subset", readDataSubset)
	}
	args = append(args, "--retry-lock", LockRetry)

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic check: %w", ctxErr)
		}
		return c.commandError("check", err, stderr.String())
	}
	return nil
}

// Forget applies the retention policy and optionally prunes unreferenced data.
func (c *Client) Forget(ctx context.Context, policy []string, prune bool) error {
	args := append([]string{"forget"}, policy...)
	if prune {
		args = append(args, "--prune")
	}
	args = append(args, "--retry-lock", LockRetry)

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic forget: %w", ctxErr)
		}
		return c.commandError("forget", err, stderr.String())
	}
	return nil
}

// Diff prints differences between two snapshots.
func (c *Client) Diff(ctx context.Context, first, second string, asJSON bool) error {
	args := []string{"diff", first, second}
	if asJSON {
		args = append(args, "--json")
	}
	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic diff: %w", ctxErr)
		}
		return c.commandError("diff", err, stderr.String())
	}
	return nil
}

// ForgetSnapshot removes one snapshot from the repository.
func (c *Client) ForgetSnapshot(ctx context.Context, snapshot string, prune bool) error {
	args := []string{"forget", snapshot}
	if prune {
		args = append(args, "--prune")
	}
	args = append(args, "--retry-lock", LockRetry)
	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic forget: %w", ctxErr)
		}
		return c.commandError("forget", err, stderr.String())
	}
	return nil
}

// CopyDest describes the destination of a restic copy.
type CopyDest struct {
	Repository   string
	PasswordFile string
	Env          map[string]string
	// CacheDir is passed to restic as RESTIC_CACHE_DIR, see Options.CacheDir.
	CacheDir string
}

// CopyTo copies snapshots to a second repository. When newOnly is true restic
// copies only snapshots missing from the destination.
func (c *Client) CopyTo(ctx context.Context, dest CopyDest, snapshots []string, newOnly bool) error {
	if dest.Repository == "" {
		return errors.New("copy destination repository is empty")
	}
	if !newOnly && len(snapshots) == 0 {
		list, err := c.Snapshots(ctx, "")
		if err != nil {
			return err
		}
		for _, snapshot := range list {
			snapshots = append(snapshots, snapshot.ID)
		}
	}

	args := []string{"copy"}
	if c.passwordFile != "" {
		args = append(args, "--from-password-file", c.passwordFile)
	}
	args = append(args, snapshots...)

	cmd := executil.Command(ctx, c.binary, args...)
	cmd.Env = append(copyEnvironment(dest), "RESTIC_FROM_REPOSITORY="+c.repository)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic copy: %w", ctxErr)
		}
		return c.commandError("copy", err, stderr.String())
	}
	return nil
}

func copyEnvironment(dest CopyDest) []string {
	env := executil.SanitizeEnv(os.Environ())
	env = append(env, "RESTIC_REPOSITORY="+dest.Repository)
	if dest.PasswordFile != "" {
		env = append(env, "RESTIC_PASSWORD_FILE="+dest.PasswordFile)
	}
	if dest.CacheDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+dest.CacheDir)
	}
	for key, value := range dest.Env {
		env = append(env, key+"="+value)
	}
	return env
}

// Init creates the repository. It fails if the repository already exists.
func (c *Client) Init(ctx context.Context) error {
	cmd := c.command(ctx, "init")
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic init: %w", ctxErr)
		}
		return c.commandError("init", err, stderr.String())
	}
	return nil
}

// Version returns the first line of "restic version".
func (c *Client) Version(ctx context.Context) (string, error) {
	cmd := c.command(ctx, "version")
	var stdout, stderr tailBuffer
	stdout.max = 4 << 10
	stderr.max = 4 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", c.commandError("version", err, stderr.String())
	}
	line, _, _ := strings.Cut(strings.TrimSpace(stdout.String()), "\n")
	return line, nil
}

// CheckAccess verifies that the repository exists and the credentials work.
func (c *Client) CheckAccess(ctx context.Context) error {
	cmd := c.command(ctx, "cat", "config")
	cmd.Stdout = io.Discard
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return c.commandError("cat config", err, stderr.String())
	}
	return nil
}

// Unlock removes stale locks. Restic keeps locks held by live processes.
func (c *Client) Unlock(ctx context.Context) error {
	cmd := c.command(ctx, "unlock")
	cmd.Stdout = io.Discard
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return c.commandError("unlock", err, stderr.String())
	}
	return nil
}

// AddKey adds a new repository key that is protected by newPasswordFile. The
// current password still opens the repository until the old key is removed.
func (c *Client) AddKey(ctx context.Context, newPasswordFile string) error {
	cmd := c.command(ctx, "key", "add", "--new-password-file", newPasswordFile)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return c.commandError("key add", err, stderr.String())
	}
	return nil
}

// Keys prints the repository keys.
func (c *Client) Keys(ctx context.Context) error {
	cmd := c.command(ctx, "key", "list")
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return c.commandError("key list", err, stderr.String())
	}
	return nil
}

// RemoveKey deletes one repository key by ID.
func (c *Client) RemoveKey(ctx context.Context, id string) error {
	cmd := c.command(ctx, "key", "remove", id)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return c.commandError("key remove", err, stderr.String())
	}
	return nil
}

// Stats describes repository usage.
type Stats struct {
	TotalSize      int64 `json:"total_size"`
	TotalFileCount int64 `json:"total_file_count"`
	TotalBlobCount int64 `json:"total_blob_count"`
	SnapshotsCount int64 `json:"snapshots_count"`
}

// Stats returns repository statistics for the given restic mode.
func (c *Client) Stats(ctx context.Context, mode string) (Stats, error) {
	return c.StatsFor(ctx, "", mode)
}

// StatsFor returns statistics for one snapshot, or the whole repository when
// snapshot is empty.
func (c *Client) StatsFor(ctx context.Context, snapshot, mode string) (Stats, error) {
	args := []string{"stats", "--json"}
	if mode != "" {
		args = append(args, "--mode", mode)
	}
	if snapshot != "" {
		args = append(args, snapshot)
	}

	var stdout bytes.Buffer
	cmd := c.command(ctx, args...)
	cmd.Stdout = &stdout
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Stats{}, c.commandError("stats", err, stderr.String())
	}

	var stats Stats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		return Stats{}, fmt.Errorf("parse restic stats: %w", err)
	}
	return stats, nil
}

// Mount exposes the repository read-only through FUSE until the context ends.
func (c *Client) Mount(ctx context.Context, target string, stdout, stderr io.Writer) error {
	cmd := c.command(ctx, "mount", target)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("restic mount: %w", err)
	}
	return nil
}

// IsLockError reports whether restic failed because the repository is locked.
func IsLockError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "already locked") ||
		strings.Contains(text, "create lock") ||
		strings.Contains(text, "lock file already exists")
}

var urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)

func (c *Client) redact(text string) string {
	text = urlUserinfo.ReplaceAllString(text, "$1***@")
	for _, value := range c.env {
		if len(value) >= 4 {
			text = strings.ReplaceAll(text, value, "***")
		}
	}
	return text
}

func (c *Client) commandError(command string, err error, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return errors.New("restic is not installed or not found in PATH")
	}
	raw := strings.TrimSpace(stderr)
	detail := c.redact(raw)
	if raw != "" && c.showSensitive {
		c.log.Debug("restic stderr", "command", command, "stderr", raw)
	} else if raw != "" && raw != detail {
		c.log.Info("restic stderr redacted; set logging.show_sensitive = true to print it", "command", command)
	}
	if detail == "" {
		return fmt.Errorf("restic %s: %w", command, err)
	}
	return fmt.Errorf("restic %s: %w: %s", command, err, detail)
}

type tailBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf.Write(p)
	if b.max > 0 && b.buf.Len() > b.max {
		data := b.buf.Bytes()
		keep := append([]byte(nil), data[len(data)-b.max:]...)
		b.buf.Reset()
		b.buf.Write(keep)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return b.buf.String()
}
