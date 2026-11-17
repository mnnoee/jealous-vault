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
	"slices"
	"strings"
	"time"
)

// Options configures a Client.
type Options struct {
	Binary       string
	Repository   string
	PasswordFile string
	Env          map[string]string
	Logger       *slog.Logger
	Output       io.Writer
}

// Client runs restic with a fixed repository and environment.
type Client struct {
	binary       string
	repository   string
	passwordFile string
	env          map[string]string
	log          *slog.Logger
	out          io.Writer
}

// New returns a Client. Missing optional values fall back to safe defaults.
func New(o Options) *Client {
	binary := o.Binary
	if binary == "" {
		binary = "restic"
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
		binary:       binary,
		repository:   o.Repository,
		passwordFile: o.PasswordFile,
		env:          o.Env,
		log:          log,
		out:          out,
	}
}

func (c *Client) environment() []string {
	env := os.Environ()
	env = append(env, "RESTIC_REPOSITORY="+c.repository)
	if c.passwordFile != "" {
		env = append(env, "RESTIC_PASSWORD_FILE="+c.passwordFile)
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

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.binary, args...)
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

// Backup copies paths into the repository and returns restic's summary.
func (c *Client) Backup(ctx context.Context, o BackupOptions) (Summary, error) {
	var summary Summary

	args := []string{"backup", "--json"}
	if o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	if o.OneFileSystem {
		args = append(args, "--one-file-system")
	}
	for _, tag := range o.Tags {
		args = append(args, "--tag", tag)
	}
	for _, pattern := range o.Excludes {
		args = append(args, "--exclude", pattern)
	}
	args = append(args, o.Paths...)

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
		return summary, commandError("backup", waitErr, stderr.String())
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
		return nil, commandError("snapshots", err, stderr.String())
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

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic restore: %w", ctxErr)
		}
		return commandError("restore", err, stderr.String())
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

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic check: %w", ctxErr)
		}
		return commandError("check", err, stderr.String())
	}
	return nil
}

// Forget applies the retention policy and optionally prunes unreferenced data.
func (c *Client) Forget(ctx context.Context, policy []string, prune bool) error {
	args := append([]string{"forget"}, policy...)
	if prune {
		args = append(args, "--prune")
	}

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.out
	var stderr tailBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("restic forget: %w", ctxErr)
		}
		return commandError("forget", err, stderr.String())
	}
	return nil
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
		return commandError("init", err, stderr.String())
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
		return "", commandError("version", err, stderr.String())
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
		return commandError("cat config", err, stderr.String())
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
		return commandError("unlock", err, stderr.String())
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

func commandError(command string, err error, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return errors.New("restic is not installed or not found in PATH")
	}
	detail := strings.TrimSpace(stderr)
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
