// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates the JealousVault agent configuration.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultPath is where the agent reads its configuration on a normal install.
	DefaultPath = "/etc/jealous-vault/config.toml"
	// DefaultStateFile stores the result of the last backup and check.
	DefaultStateFile = "/var/lib/jealous-vault/state.json"
	// DefaultResticBinary is resolved through PATH.
	DefaultResticBinary = "restic"
)

// Config is the on-disk configuration. Durations stay strings here so that the
// TOML file reads naturally, and are parsed into time.Duration fields.
type Config struct {
	Host         string    `toml:"host"`
	StateFile    string    `toml:"state_file"`
	LockFile     string    `toml:"lock_file"`
	AlertsFile   string    `toml:"alerts_file"`
	ResticBinary string    `toml:"restic_binary"`
	Repository   Repo      `toml:"repository"`
	Backup       Backup    `toml:"backup"`
	Retention    Retention `toml:"retention"`
	Schedule     Schedule  `toml:"schedule"`
	Freshness    Freshness `toml:"freshness"`
	Verify       Verify    `toml:"verify"`
	Proxmox      Proxmox   `toml:"proxmox"`
	Copy         Copy      `toml:"copy"`
	Check        Check     `toml:"check"`
	Restore      Restore   `toml:"restore"`
	Notify       Notify    `toml:"notify"`

	Path string `toml:"-"`

	BackupTimeout   time.Duration `toml:"-"`
	CheckTimeout    time.Duration `toml:"-"`
	NotifyTimeout   time.Duration `toml:"-"`
	RandomizedDelay time.Duration `toml:"-"`
	FreshnessMaxAge time.Duration `toml:"-"`
	VerifyTimeout   time.Duration `toml:"-"`
	VerifyMaxSize   int64         `toml:"-"`
	VerifyMinFree   int64         `toml:"-"`
	RetryDelay      time.Duration `toml:"-"`
	EmailPassword   string        `toml:"-"`
	EmailTimeout    time.Duration `toml:"-"`
}

// Repo describes the restic repository and its credentials.
type Repo struct {
	URL            string            `toml:"url"`
	PasswordFile   string            `toml:"password_file"`
	EnvFile        string            `toml:"env_file"`
	SFTPKeyFile    string            `toml:"sftp_key_file"`
	SFTPKnownHosts string            `toml:"sftp_known_hosts"`
	SFTPConfig     string            `toml:"sftp_config"`
	Env            map[string]string `toml:"env"`
}

// Backup describes what to copy and how.
type Backup struct {
	Profile       string   `toml:"profile"`
	Paths         []string `toml:"paths"`
	Excludes      []string `toml:"excludes"`
	Tags          []string `toml:"tags"`
	PreHooks      []string `toml:"pre_hooks"`
	PostHooks     []string `toml:"post_hooks"`
	OneFileSystem bool     `toml:"one_file_system"`
	Timeout       string   `toml:"timeout"`
	Attempts      int      `toml:"attempts"`
	RetryDelay    string   `toml:"retry_delay"`
	LimitUpload   int      `toml:"limit_upload"`
	Compression   string   `toml:"compression"`
	PackSize      int      `toml:"pack_size"`
}

// Retention is passed to restic forget.
type Retention struct {
	KeepLast    int      `toml:"keep_last"`
	KeepDaily   int      `toml:"keep_daily"`
	KeepWeekly  int      `toml:"keep_weekly"`
	KeepMonthly int      `toml:"keep_monthly"`
	KeepWithin  string   `toml:"keep_within"`
	KeepTag     []string `toml:"keep_tag"`
	Prune       bool     `toml:"prune"`
}

// Schedule drives the generated systemd timer.
type Schedule struct {
	OnCalendar      string `toml:"on_calendar"`
	RandomizedDelay string `toml:"randomized_delay"`
}

// Freshness controls the stale backup check.
type Freshness struct {
	MaxAge             string `toml:"max_age"`
	RespectBackupTimer bool   `toml:"respect_backup_timer"`
	Schedule           string `toml:"schedule"`
}

// Verify controls the scheduled test restore.
type Verify struct {
	Enabled  bool   `toml:"enabled"`
	Level    string `toml:"level"`
	MaxSize  string `toml:"max_size"`
	MinFree  string `toml:"min_free"`
	Timeout  string `toml:"timeout"`
	Schedule string `toml:"schedule"`
	PostHook string `toml:"post_hook"`
}

// Proxmox controls vzdump and VM restore.
type Proxmox struct {
	Enabled  bool   `toml:"enabled"`
	Schedule string `toml:"schedule"`
	DumpDir  string `toml:"dump_dir"`
	Mode     string `toml:"mode"`
	Compress string `toml:"compress"`
	KeepLast int    `toml:"keep_last"`
	VMs      []int  `toml:"vms"`
	All      bool   `toml:"all"`
	Storage  string `toml:"storage"`
}

// Copy controls the secondary repository.
type Copy struct {
	Enabled      bool              `toml:"enabled"`
	Schedule     string            `toml:"schedule"`
	Repository   string            `toml:"repository"`
	PasswordFile string            `toml:"password_file"`
	EnvFile      string            `toml:"env_file"`
	Env          map[string]string `toml:"env"`
	NewOnly      bool              `toml:"new_only"`
}

// Check controls the integrity check timer.
type Check struct {
	ReadDataSubset string `toml:"read_data_subset"`
	Timeout        string `toml:"timeout"`
}

// Restore hooks run around a restore, for example to stop and start services.
type Restore struct {
	PreHooks  []string `toml:"pre_hooks"`
	PostHooks []string `toml:"post_hooks"`
}

// Notify lists outbound notification channels.
type Notify struct {
	Webhooks         []string `toml:"webhooks"`
	PingURL          string   `toml:"ping_url"`
	FreshnessPingURL string   `toml:"freshness_ping_url"`
	TelegramToken    string   `toml:"telegram_token"`
	TelegramChatID   string   `toml:"telegram_chat_id"`
	TelegramChatIDs  []string `toml:"telegram_chat_ids"`
	NotifyOnSuccess  bool     `toml:"notify_on_success"`
	Timeout          string   `toml:"timeout"`
	Email            Email    `toml:"email"`
}

// Email configures the email channel.
type Email struct {
	Enabled      bool     `toml:"enabled"`
	Mode         string   `toml:"mode"`
	From         string   `toml:"from"`
	To           []string `toml:"to"`
	Host         string   `toml:"host"`
	Port         int      `toml:"port"`
	Username     string   `toml:"username"`
	PasswordFile string   `toml:"password_file"`
	PasswordEnv  string   `toml:"password_env"`
	Security     string   `toml:"security"`
	Timeout      string   `toml:"timeout"`
}

// Default returns a configuration with every non-secret value filled in.
func Default() *Config {
	host, _ := os.Hostname()
	return &Config{
		Host:         host,
		StateFile:    DefaultStateFile,
		ResticBinary: DefaultResticBinary,
		Retention: Retention{
			KeepLast:    3,
			KeepDaily:   7,
			KeepWeekly:  4,
			KeepMonthly: 6,
			Prune:       true,
		},
		Schedule: Schedule{
			OnCalendar:      "*-*-* 03:30:00",
			RandomizedDelay: "1h",
		},
		Freshness: Freshness{
			MaxAge:             "25h",
			RespectBackupTimer: true,
			Schedule:           "*-*-* 00,06,12,18:15:00",
		},
		Verify: Verify{
			Enabled:  true,
			Level:    "files",
			MaxSize:  "10GB",
			MinFree:  "20GB",
			Timeout:  "2h",
			Schedule: "Sun *-*-* 05:00:00",
		},
		Copy: Copy{
			Schedule: "*-*-* 05:00:00",
			NewOnly:  true,
		},
		Proxmox: Proxmox{
			Schedule: "*-*-* 02:00:00",
			DumpDir:  "/var/lib/vz/dump/jealous-vault",
			Mode:     "snapshot",
			Compress: "zstd",
			KeepLast: 1,
			All:      true,
			Storage:  "local-lvm",
		},
		Backup: Backup{Profile: "host", Timeout: "6h", Attempts: 3, RetryDelay: "1m", Compression: "auto"},
		Check:  Check{Timeout: "2h"},
		Notify: Notify{
			Timeout: "10s",
			Email: Email{
				Mode:     "local",
				Port:     25,
				Security: "starttls",
				Timeout:  "15s",
			},
		},
	}
}

// Load reads path, applies defaults and validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse applies defaults to data and validates the result. It does not touch
// the filesystem, so it can validate generated configuration before writing.
func Parse(path string, data []byte) (*Config, error) {
	cfg := Default()

	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if unknown := md.Undecoded(); len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for _, k := range unknown {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown keys in %s: %s", path, strings.Join(keys, ", "))
	}

	cfg.Path = path
	if err := cfg.finish(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) finish() error {
	if c.Host == "" {
		host, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("host is empty and hostname is unavailable: %w", err)
		}
		c.Host = host
	}
	if c.StateFile == "" {
		c.StateFile = DefaultStateFile
	}
	if c.LockFile == "" {
		c.LockFile = filepath.Join(filepath.Dir(c.StateFile), "backup.lock")
	}
	if c.AlertsFile == "" {
		c.AlertsFile = filepath.Join(filepath.Dir(c.StateFile), "alerts.json")
	}
	if c.ResticBinary == "" {
		c.ResticBinary = DefaultResticBinary
	}
	if c.Schedule.OnCalendar == "" {
		c.Schedule.OnCalendar = "*-*-* 03:30:00"
	}
	if c.Schedule.RandomizedDelay == "" {
		c.Schedule.RandomizedDelay = "1h"
	}
	if c.Freshness.MaxAge == "" {
		c.Freshness.MaxAge = "25h"
	}
	if c.Freshness.Schedule == "" {
		c.Freshness.Schedule = "*-*-* 00,06,12,18:15:00"
	}
	if c.Verify.Level == "" {
		c.Verify.Level = "files"
	}
	if c.Verify.MaxSize == "" {
		c.Verify.MaxSize = "10GB"
	}
	if c.Verify.MinFree == "" {
		c.Verify.MinFree = "20GB"
	}
	if c.Verify.Timeout == "" {
		c.Verify.Timeout = "2h"
	}
	if c.Verify.Schedule == "" {
		c.Verify.Schedule = "Sun *-*-* 05:00:00"
	}
	if c.Proxmox.Schedule == "" {
		c.Proxmox.Schedule = "*-*-* 02:00:00"
	}
	if c.Proxmox.DumpDir == "" {
		c.Proxmox.DumpDir = "/var/lib/vz/dump/jealous-vault"
	}
	if c.Proxmox.Mode == "" {
		c.Proxmox.Mode = "snapshot"
	}
	if c.Proxmox.Compress == "" {
		c.Proxmox.Compress = "zstd"
	}
	if c.Proxmox.Storage == "" {
		c.Proxmox.Storage = "local-lvm"
	}
	if c.Copy.Schedule == "" {
		c.Copy.Schedule = "*-*-* 05:00:00"
	}
	if c.Copy.EnvFile != "" {
		env, err := LoadEnvFile(c.Copy.EnvFile)
		if err != nil {
			return fmt.Errorf("copy env file: %w", err)
		}
		if c.Copy.Env == nil {
			c.Copy.Env = env
		}
	}
	if c.Backup.Profile == "" {
		c.Backup.Profile = "host"
	}
	if c.Repository.SFTPKeyFile != "" {
		if c.Repository.SFTPConfig == "" {
			c.Repository.SFTPConfig = "/etc/jealous-vault/ssh_config"
		}
		if c.Repository.SFTPKnownHosts == "" {
			c.Repository.SFTPKnownHosts = "/etc/jealous-vault/known_hosts"
		}
	}
	if c.Backup.Timeout == "" {
		c.Backup.Timeout = "6h"
	}
	if c.Backup.Attempts == 0 {
		c.Backup.Attempts = 3
	}
	if c.Backup.RetryDelay == "" {
		c.Backup.RetryDelay = "1m"
	}
	if c.Backup.Compression == "" {
		c.Backup.Compression = "auto"
	}
	if c.Check.Timeout == "" {
		c.Check.Timeout = "2h"
	}
	if c.Notify.Timeout == "" {
		c.Notify.Timeout = "10s"
	}
	if c.Notify.Email.Mode == "" {
		c.Notify.Email.Mode = "local"
	}
	if c.Notify.Email.Port == 0 {
		if c.Notify.Email.Mode == "smtp" {
			c.Notify.Email.Port = 587
		} else {
			c.Notify.Email.Port = 25
		}
	}
	if c.Notify.Email.Security == "" {
		c.Notify.Email.Security = "starttls"
	}
	if c.Notify.Email.Timeout == "" {
		c.Notify.Email.Timeout = "15s"
	}
	if c.Notify.Email.Enabled {
		if c.Notify.Email.PasswordFile != "" && c.Notify.Email.PasswordEnv != "" {
			return errors.New("notify.email: set only one of password_file and password_env")
		}
		if c.Notify.Email.PasswordFile != "" {
			if err := CheckSecretFile("notify.email.password_file", c.Notify.Email.PasswordFile); err != nil {
				return err
			}
			data, err := os.ReadFile(c.Notify.Email.PasswordFile)
			if err != nil {
				return fmt.Errorf("notify.email.password_file: %w", err)
			}
			c.EmailPassword = strings.TrimSpace(string(data))
		} else if c.Notify.Email.PasswordEnv != "" {
			c.EmailPassword = os.Getenv(c.Notify.Email.PasswordEnv)
			if c.EmailPassword == "" {
				return fmt.Errorf("notify.email.password_env %s is empty", c.Notify.Email.PasswordEnv)
			}
		}
	}

	if c.Repository.EnvFile != "" {
		env, err := LoadEnvFile(c.Repository.EnvFile)
		if err != nil {
			return fmt.Errorf("env file: %w", err)
		}
		if c.Repository.Env == nil {
			c.Repository.Env = make(map[string]string, len(env))
		}
		for k, v := range env {
			if _, ok := c.Repository.Env[k]; !ok {
				c.Repository.Env[k] = v
			}
		}
	}

	var err error
	if c.BackupTimeout, err = parseDuration("backup.timeout", c.Backup.Timeout); err != nil {
		return err
	}
	if c.CheckTimeout, err = parseDuration("check.timeout", c.Check.Timeout); err != nil {
		return err
	}
	if c.NotifyTimeout, err = parseDuration("notify.timeout", c.Notify.Timeout); err != nil {
		return err
	}
	if c.EmailTimeout, err = parseDuration("notify.email.timeout", c.Notify.Email.Timeout); err != nil {
		return err
	}
	if c.RandomizedDelay, err = parseDuration("schedule.randomized_delay", c.Schedule.RandomizedDelay); err != nil {
		return err
	}
	if c.FreshnessMaxAge, err = parseDuration("freshness.max_age", c.Freshness.MaxAge); err != nil {
		return err
	}
	if c.VerifyTimeout, err = parseDuration("verify.timeout", c.Verify.Timeout); err != nil {
		return err
	}
	if c.VerifyMaxSize, err = ParseSize(c.Verify.MaxSize); err != nil {
		return fmt.Errorf("verify.max_size: %w", err)
	}
	if c.VerifyMinFree, err = ParseSize(c.Verify.MinFree); err != nil {
		return fmt.Errorf("verify.min_free: %w", err)
	}
	if c.RetryDelay, err = parseDuration("backup.retry_delay", c.Backup.RetryDelay); err != nil {
		return err
	}

	return c.Validate()
}

// Validate checks the parts that would otherwise fail at backup time.
func (c *Config) Validate() error {
	if c.Repository.URL == "" {
		return errors.New("repository.url is required")
	}
	if c.Repository.PasswordFile == "" && c.Repository.Env["RESTIC_PASSWORD"] == "" {
		return errors.New("repository.password_file or RESTIC_PASSWORD in repository.env is required")
	}
	for key := range c.Repository.Env {
		if err := ValidateEnvKey(key); err != nil {
			return fmt.Errorf("repository.env: %w", err)
		}
	}
	for key := range c.Copy.Env {
		if err := ValidateEnvKey(key); err != nil {
			return fmt.Errorf("copy.env: %w", err)
		}
	}
	sftpFields := c.Repository.SFTPKeyFile != "" || c.Repository.SFTPKnownHosts != "" || c.Repository.SFTPConfig != ""
	if sftpFields && !strings.HasPrefix(c.Repository.URL, "sftp:") {
		return errors.New("sftp_key_file, sftp_known_hosts and sftp_config require an sftp repository url")
	}
	for _, item := range []struct {
		key  string
		path string
	}{
		{"state_file", c.StateFile},
		{"lock_file", c.LockFile},
		{"alerts_file", c.AlertsFile},
		{"repository.password_file", c.Repository.PasswordFile},
		{"repository.env_file", c.Repository.EnvFile},
		{"repository.sftp_key_file", c.Repository.SFTPKeyFile},
		{"repository.sftp_known_hosts", c.Repository.SFTPKnownHosts},
		{"repository.sftp_config", c.Repository.SFTPConfig},
		{"copy.password_file", c.Copy.PasswordFile},
		{"copy.env_file", c.Copy.EnvFile},
	} {
		if item.path != "" && !filepath.IsAbs(item.path) {
			return fmt.Errorf("%s %q must be absolute", item.key, item.path)
		}
	}
	if c.Retention.KeepWithin != "" && strings.ContainsAny(c.Retention.KeepWithin, " \t\n") {
		return errors.New("retention.keep_within must not contain whitespace")
	}
	for i, tag := range c.Retention.KeepTag {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("retention.keep_tag[%d] is empty", i)
		}
	}
	switch c.Backup.Profile {
	case "host", "vm-dumps", "custom":
	default:
		return fmt.Errorf("backup.profile %q must be host, vm-dumps or custom", c.Backup.Profile)
	}
	if len(c.Backup.Paths) == 0 {
		return errors.New("backup.paths is empty")
	}
	for _, p := range c.Backup.Paths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("backup path %q must be absolute", p)
		}
	}
	for i, p := range c.Backup.Excludes {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("backup.excludes[%d] is empty", i)
		}
	}
	if c.Schedule.OnCalendar == "" {
		return errors.New("schedule.on_calendar is empty")
	}
	if c.RandomizedDelay < 0 {
		return errors.New("schedule.randomized_delay must not be negative")
	}
	if c.Freshness.MaxAge == "" {
		return errors.New("freshness.max_age is empty")
	}
	if c.FreshnessMaxAge <= 0 {
		return errors.New("freshness.max_age must be positive")
	}
	if c.Freshness.Schedule == "" {
		return errors.New("freshness.schedule is empty")
	}
	switch c.Verify.Level {
	case "files", "service":
	default:
		return fmt.Errorf("verify.level %q must be files or service", c.Verify.Level)
	}
	if c.Verify.Level == "service" && c.Verify.PostHook == "" {
		return errors.New("verify.post_hook is required for the service level")
	}
	if c.VerifyMaxSize <= 0 {
		return errors.New("verify.max_size must be positive")
	}
	if c.VerifyMinFree <= 0 {
		return errors.New("verify.min_free must be positive")
	}
	if c.VerifyTimeout <= 0 {
		return errors.New("verify.timeout must be positive")
	}
	if c.Verify.Enabled && c.Verify.Schedule == "" {
		return errors.New("verify.schedule is empty")
	}
	switch c.Proxmox.Mode {
	case "snapshot", "suspend", "stop":
	default:
		return fmt.Errorf("proxmox.mode %q must be snapshot, suspend or stop", c.Proxmox.Mode)
	}
	switch c.Proxmox.Compress {
	case "zstd", "gzip", "lzo", "0", "none":
	default:
		return fmt.Errorf("proxmox.compress %q must be zstd, gzip, lzo or none", c.Proxmox.Compress)
	}
	if !filepath.IsAbs(c.Proxmox.DumpDir) {
		return fmt.Errorf("proxmox.dump_dir %q must be absolute", c.Proxmox.DumpDir)
	}
	if c.Proxmox.KeepLast < 0 {
		return errors.New("proxmox.keep_last must not be negative")
	}
	if c.Proxmox.Enabled && c.Proxmox.Schedule == "" {
		return errors.New("proxmox.schedule is empty")
	}
	if c.Proxmox.Storage == "" {
		return errors.New("proxmox.storage is empty")
	}
	if c.Copy.Enabled {
		if c.Copy.Repository == "" {
			return errors.New("copy.repository is required when copy is enabled")
		}
		if c.Copy.PasswordFile == "" {
			return errors.New("copy.password_file is required when copy is enabled")
		}
		if c.Copy.PasswordFile == c.Repository.PasswordFile {
			return errors.New("copy.password_file must differ from the primary repository password")
		}
		if c.Copy.Schedule == "" {
			return errors.New("copy.schedule is empty")
		}
	}
	if c.BackupTimeout <= 0 {
		return errors.New("backup.timeout must be positive")
	}
	if c.Backup.Attempts < 1 {
		return errors.New("backup.attempts must be at least 1")
	}
	if c.RetryDelay < 0 {
		return errors.New("backup.retry_delay must not be negative")
	}
	if c.Backup.LimitUpload < 0 {
		return errors.New("backup.limit_upload must not be negative")
	}
	switch c.Backup.Compression {
	case "auto", "off", "max":
	default:
		return fmt.Errorf("backup.compression %q must be auto, off or max", c.Backup.Compression)
	}
	if c.Backup.PackSize < 0 {
		return errors.New("backup.pack_size must not be negative")
	}
	if c.CheckTimeout <= 0 {
		return errors.New("check.timeout must be positive")
	}
	chats := append([]string(nil), c.Notify.TelegramChatIDs...)
	if c.Notify.TelegramChatID != "" {
		chats = append(chats, c.Notify.TelegramChatID)
	}
	if (c.Notify.TelegramToken == "") != (len(chats) == 0) {
		return errors.New("notify.telegram_token and at least one chat id must be set together")
	}
	for _, chat := range chats {
		if err := validateTelegramChat(chat); err != nil {
			return err
		}
	}
	for _, u := range c.Notify.Webhooks {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("notify.webhooks entry %q must be an http or https URL", u)
		}
	}
	if c.Notify.PingURL != "" &&
		!strings.HasPrefix(c.Notify.PingURL, "http://") &&
		!strings.HasPrefix(c.Notify.PingURL, "https://") {
		return fmt.Errorf("notify.ping_url %q must be an http or https URL", c.Notify.PingURL)
	}
	if c.Notify.Email.Enabled {
		if err := validateEmail(c.Notify.Email, c.EmailPassword); err != nil {
			return err
		}
	}
	if c.Notify.FreshnessPingURL != "" &&
		!strings.HasPrefix(c.Notify.FreshnessPingURL, "http://") &&
		!strings.HasPrefix(c.Notify.FreshnessPingURL, "https://") {
		return fmt.Errorf("notify.freshness_ping_url %q must be an http or https URL", c.Notify.FreshnessPingURL)
	}
	return nil
}

func parseDuration(key, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// RetentionArgs returns restic forget flags for the configured policy.
// It returns nil when no retention rule is set.
func (c *Config) RetentionArgs() []string {
	var args []string
	add := func(flag string, n int) {
		if n > 0 {
			args = append(args, flag, strconv.Itoa(n))
		}
	}
	add("--keep-last", c.Retention.KeepLast)
	add("--keep-daily", c.Retention.KeepDaily)
	add("--keep-weekly", c.Retention.KeepWeekly)
	add("--keep-monthly", c.Retention.KeepMonthly)
	if c.Retention.KeepWithin != "" {
		args = append(args, "--keep-within", c.Retention.KeepWithin)
	}
	for _, tag := range c.Retention.KeepTag {
		args = append(args, "--keep-tag", tag)
	}
	return args
}

// ResticExtraOptions returns global restic options for this repository.
func (c *Config) ResticExtraOptions() []string {
	if c.Repository.SFTPConfig == "" || !strings.HasPrefix(c.Repository.URL, "sftp:") {
		return nil
	}
	return []string{"-o", "sftp.args=-F " + c.Repository.SFTPConfig}
}

// HasRetention reports whether forget should run after a backup.
func (c *Config) HasRetention() bool {
	return len(c.RetentionArgs()) > 0
}

// RedactedRepository returns the repository URL with userinfo removed, so it
// can be logged and printed without leaking credentials.
func (c *Config) RedactedRepository() string {
	return RedactURL(c.Repository.URL)
}

// RedactURL hides the userinfo part of a URL that carries a scheme prefix,
// for example s3:https://key:secret@host/bucket.
func RedactURL(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	prefix, rest := raw[:i+3], raw[i+3:]
	authority := rest
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		authority = rest[:slash]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return raw
	}
	return prefix + "***@" + rest[at+1:]
}

// LoadEnvFile reads simple KEY=VALUE lines. Blank lines and lines starting
// with # are ignored. A leading "export " is accepted.
func LoadEnvFile(path string) (map[string]string, error) {
	if err := CheckSecretFile("env file", path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", i+1)
		}
		if err := ValidateEnvKey(key); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		env[key] = value
	}
	return env, nil
}

// GeneratePassword writes a fresh random repository password to path.
// It refuses to overwrite an existing file.
func GeneratePassword(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	if _, err := f.WriteString(base64.RawURLEncoding.EncodeToString(buf) + "\n"); err != nil {
		return err
	}
	return f.Sync()
}
