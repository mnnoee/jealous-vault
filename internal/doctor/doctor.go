// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package doctor performs read-only preflight checks before a backup.
package doctor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/notify"
	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/systemd"
)

// Level is the outcome of one check.
type Level string

// Check levels.
const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

// Result is one check line.
type Result struct {
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
}

// Report is the full doctor output.
type Report struct {
	Results []Result `json:"results"`
	Failed  bool     `json:"failed"`
}

// testedResticMinor is the restic series the agent is tested with.
const testedResticMinor = "0.18"

// Options configures external commands.
type Options struct {
	Systemctl string
	Logger    *slog.Logger
}

// Run checks the host, the config and the repository without changing data.
func Run(ctx context.Context, cfg *config.Config, opts Options) Report {
	var report Report
	add := func(name string, level Level, detail string) {
		report.Results = append(report.Results, Result{Name: name, Level: level, Detail: detail})
		if level == LevelFail {
			report.Failed = true
		}
	}

	checkConfigFile(cfg, add)
	checkPassword(cfg, add)
	checkSecretFiles(cfg, add)

	client := restic.New(restic.Options{
		Binary:       cfg.ResticBinary,
		Repository:   cfg.Repository.URL,
		PasswordFile: cfg.Repository.PasswordFile,
		Env:          cfg.Repository.Env,
		Extra:        cfg.ResticExtraOptions(),
		Logger:       opts.Logger,
	})

	resticAvailable := checkRestic(ctx, cfg, client, add)
	if resticAvailable {
		if err := client.CheckAccess(ctx); err != nil {
			add("repository", LevelFail, err.Error())
		} else {
			add("repository", LevelOK, "accessible, password accepted")
		}
	} else {
		add("repository", LevelFail, "skipped, restic is not available")
	}

	checkPaths(cfg, add)
	checkBackupSecrets(cfg, add)
	checkDumps(cfg, add)
	stateDir := checkStateDir(cfg, add)
	checkFreeSpace(stateDir, add)
	checkSystemd(ctx, opts.Systemctl, cfg.Verify.Enabled, cfg.Job, add)
	checkS3Credentials(cfg, add)
	checkEmail(ctx, cfg, add)
	checkRetention(cfg, add)
	if cfg.Proxmox.Enabled {
		checkProxmox(ctx, cfg, add)
	}
	if cfg.Copy.Enabled {
		checkCopy(cfg, add)
	}

	return report
}

func checkConfigFile(cfg *config.Config, add func(string, Level, string)) {
	if err := config.CheckSecretFile("config", cfg.Path); err != nil {
		add("config", LevelFail, err.Error())
		return
	}
	add("config", LevelOK, cfg.Path)
}

func checkPassword(cfg *config.Config, add func(string, Level, string)) {
	if cfg.Repository.PasswordFile == "" {
		if cfg.Repository.Env["RESTIC_PASSWORD"] != "" {
			add("password", LevelOK, "provided by RESTIC_PASSWORD")
			return
		}
		add("password", LevelFail, "no password file and no RESTIC_PASSWORD")
		return
	}
	info, err := os.Stat(cfg.Repository.PasswordFile)
	if err != nil {
		add("password", LevelFail, err.Error())
		return
	}
	if info.Size() == 0 {
		add("password", LevelFail, cfg.Repository.PasswordFile+" is empty")
		return
	}
	if err := config.CheckSecretFile("password", cfg.Repository.PasswordFile); err != nil {
		add("password", LevelFail, err.Error())
		return
	}
	add("password", LevelOK, cfg.Repository.PasswordFile)
}

func checkSecretFiles(cfg *config.Config, add func(string, Level, string)) {
	checks := []struct {
		label string
		path  string
	}{
		{"repository.env_file", cfg.Repository.EnvFile},
		{"repository.sftp_key_file", cfg.Repository.SFTPKeyFile},
		{"copy.password_file", cfg.Copy.PasswordFile},
		{"copy.env_file", cfg.Copy.EnvFile},
		{"notify.email.password_file", cfg.Notify.Email.PasswordFile},
	}
	for _, check := range checks {
		if check.path == "" {
			continue
		}
		if err := config.CheckSecretFile(check.label, check.path); err != nil {
			add(check.label, LevelFail, err.Error())
		}
	}
}

func checkBackupSecrets(cfg *config.Config, add func(string, Level, string)) {
	secrets := []string{
		cfg.Repository.PasswordFile,
		cfg.Repository.EnvFile,
		cfg.Repository.SFTPKeyFile,
		cfg.Copy.PasswordFile,
		cfg.Copy.EnvFile,
		cfg.Notify.Email.PasswordFile,
	}
	if p := cfg.Backup.Dumps.Postgres; p != nil && p.Enabled {
		secrets = append(secrets, p.PasswordFile)
	}
	if m := cfg.Backup.Dumps.MySQL; m != nil && m.Enabled {
		secrets = append(secrets, m.PasswordFile)
	}
	var exposed []string
	for _, secret := range secrets {
		if secret == "" || secretExcluded(secret, cfg.Backup.Excludes) {
			continue
		}
		for _, root := range cfg.Backup.Paths {
			if root == "/" || secret == root || strings.HasPrefix(secret, strings.TrimSuffix(root, "/")+"/") {
				exposed = append(exposed, secret)
				break
			}
		}
	}
	if len(exposed) > 0 {
		add("backup secrets", LevelWarn, "secret files are inside backup paths: "+strings.Join(exposed, ", "))
	}
}

func secretExcluded(path string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSuffix(pattern, "/")
		if pattern == "" {
			continue
		}
		if path == pattern || strings.HasPrefix(path, pattern+"/") {
			return true
		}
	}
	return false
}

func checkDumps(cfg *config.Config, add func(string, Level, string)) {
	enabled := false
	if p := cfg.Backup.Dumps.Postgres; p != nil && p.Enabled {
		enabled = true
		checkDumpBackend("postgres", p.Binary, p.PasswordFile, add)
	}
	if m := cfg.Backup.Dumps.MySQL; m != nil && m.Enabled {
		enabled = true
		checkDumpBackend("mysql", m.Binary, m.PasswordFile, add)
	}
	if !enabled {
		return
	}
	info, err := os.Stat(cfg.Backup.Dumps.Dir)
	switch {
	case os.IsNotExist(err):
		add("dump dir", LevelWarn, cfg.Backup.Dumps.Dir+" does not exist yet, it will be created")
	case err != nil:
		add("dump dir", LevelFail, err.Error())
	case !info.IsDir():
		add("dump dir", LevelFail, cfg.Backup.Dumps.Dir+" is not a directory")
	default:
		add("dump dir", LevelOK, cfg.Backup.Dumps.Dir+" is ready")
	}
}

func checkDumpBackend(name, binary, password string, add func(string, Level, string)) {
	if _, err := exec.LookPath(binary); err != nil {
		add("dump "+name, LevelFail, binary+" not found in PATH")
	}
	if password != "" {
		if _, err := os.Stat(password); err != nil {
			add("dump "+name, LevelFail, err.Error())
		}
	}
}

func checkRestic(ctx context.Context, cfg *config.Config, client *restic.Client, add func(string, Level, string)) bool {
	if _, err := exec.LookPath(cfg.ResticBinary); err != nil {
		add("restic", LevelFail, fmt.Sprintf("%s not found in PATH", cfg.ResticBinary))
		return false
	}
	version, err := client.Version(ctx)
	if err != nil {
		add("restic", LevelFail, err.Error())
		return false
	}
	minor, ok := resticMinor(version)
	if !ok {
		add("restic", LevelWarn, version+" (cannot parse version)")
		return true
	}
	if minor != testedResticMinor {
		add("restic", LevelWarn, fmt.Sprintf("%s, tested with %s.x", version, testedResticMinor))
		return true
	}
	add("restic", LevelOK, version)
	return true
}

func resticMinor(version string) (string, bool) {
	fields := strings.Fields(version)
	if len(fields) < 2 {
		return "", false
	}
	parts := strings.Split(fields[1], ".")
	if len(parts) < 2 {
		return "", false
	}
	return parts[0] + "." + parts[1], true
}

func checkPaths(cfg *config.Config, add func(string, Level, string)) {
	var missing []string
	for _, p := range cfg.Backup.Paths {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		add("paths", LevelFail, "missing: "+strings.Join(missing, ", "))
		return
	}
	add("paths", LevelOK, fmt.Sprintf("%d paths exist", len(cfg.Backup.Paths)))
}

func checkStateDir(cfg *config.Config, add func(string, Level, string)) string {
	dir := filepath.Dir(cfg.StateFile)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		add("state", LevelFail, err.Error())
		return dir
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		add("state", LevelFail, fmt.Sprintf("%s is not writable: %v", dir, err))
		return dir
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	add("state", LevelOK, dir+" is writable")
	return dir
}

func checkFreeSpace(dir string, add func(string, Level, string)) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		add("disk", LevelWarn, "cannot read free space: "+err.Error())
		return
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	const minimum = 1 << 30
	if free < minimum {
		add("disk", LevelWarn, fmt.Sprintf("%s free, less than 1 GiB", humanBytes(free)))
		return
	}
	add("disk", LevelOK, humanBytes(free)+" free")
}

func checkSystemd(ctx context.Context, systemctl string, verifyEnabled bool, job string, add func(string, Level, string)) {
	if systemctl == "" {
		systemctl = "systemctl"
	}
	if _, err := exec.LookPath(systemctl); err != nil {
		if _, rcErr := exec.LookPath("rc-update"); rcErr == nil {
			checkOpenRC(ctx, job, add)
			return
		}
		add("systemd", LevelWarn, "systemctl not found, timers not checked")
		return
	}

	var problems []string
	timers := []string{
		systemd.BackupTimerName(job),
		systemd.CheckTimerName(job),
		systemd.FreshnessTimerName(job),
	}
	if verifyEnabled {
		timers = append(timers, systemd.VerifyTimerName(job))
	}
	for _, timer := range timers {
		if err := systemctlRun(ctx, systemctl, "is-enabled", timer); err != nil {
			problems = append(problems, timer+" is not enabled")
			continue
		}
		if err := systemctlRun(ctx, systemctl, "is-active", timer); err != nil {
			problems = append(problems, timer+" is not active")
		}
	}
	if len(problems) > 0 {
		add("systemd", LevelWarn, strings.Join(problems, ", "))
		return
	}
	add("systemd", LevelOK, "configured timers are enabled and active")
}

func checkOpenRC(ctx context.Context, job string, add func(string, Level, string)) {
	var missing []string
	for _, path := range []string{
		filepath.Join("/etc/periodic/daily", systemd.PeriodicName("backup", job)),
		filepath.Join("/etc/periodic/weekly", systemd.PeriodicName("check", job)),
	} {
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		add("init", LevelWarn, "periodic jobs missing: "+strings.Join(missing, ", "))
		return
	}
	cmd := exec.CommandContext(ctx, "rc-service", "crond", "status")
	if err := cmd.Run(); err != nil {
		add("init", LevelWarn, "OpenRC periodic jobs installed but crond is not running")
		return
	}
	add("init", LevelOK, "OpenRC periodic jobs installed and crond is running")
}

func systemctlRun(ctx context.Context, systemctl string, args ...string) error {
	cmd := exec.CommandContext(ctx, systemctl, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func checkS3Credentials(cfg *config.Config, add func(string, Level, string)) {
	if !strings.HasPrefix(cfg.Repository.URL, "s3:") {
		return
	}
	env := cfg.Repository.Env
	if env["AWS_ACCESS_KEY_ID"] != "" && env["AWS_SECRET_ACCESS_KEY"] != "" {
		add("s3", LevelOK, "static AWS credentials are set; the repository check verifies them with a real request")
		return
	}
	for _, key := range []string{"AWS_PROFILE", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		if env[key] != "" {
			add("s3", LevelOK, "ambient AWS credentials are configured; the repository check verifies them")
			return
		}
	}
	add("s3", LevelOK, "no static keys; restic will use the instance role, verified by the repository check")
}

func checkProxmox(ctx context.Context, cfg *config.Config, add func(string, Level, string)) {
	var missing []string
	for _, bin := range []string{"vzdump", "qm", "pct"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		add("proxmox", LevelFail, "missing commands: "+strings.Join(missing, ", "))
		return
	}
	info, err := os.Stat(cfg.Proxmox.DumpDir)
	switch {
	case os.IsNotExist(err):
		add("proxmox", LevelWarn, cfg.Proxmox.DumpDir+" does not exist yet, install will create it")
	case err != nil:
		add("proxmox", LevelFail, err.Error())
	case !info.IsDir():
		add("proxmox", LevelFail, cfg.Proxmox.DumpDir+" is not a directory")
	default:
		add("proxmox", LevelOK, "commands and dump directory are present")
	}

	cmd := exec.CommandContext(ctx, "pvesm", "status")
	if out, err := cmd.CombinedOutput(); err != nil {
		add("proxmox storage", LevelWarn, strings.TrimSpace(string(out)))
	} else {
		add("proxmox storage", LevelOK, "pvesm status is readable")
	}
}

func checkEmail(ctx context.Context, cfg *config.Config, add func(string, Level, string)) {
	if !cfg.Notify.Email.Enabled {
		return
	}
	if err := notify.CheckEmail(ctx, notify.EmailOptionsFromConfig(cfg)); err != nil {
		add("email", LevelFail, err.Error())
		return
	}
	add("email", LevelOK, "MTA or SMTP relay is reachable")
}

func checkCopy(cfg *config.Config, add func(string, Level, string)) {
	if cfg.Copy.Repository == "" {
		add("copy", LevelFail, "copy.repository is empty")
		return
	}
	if cfg.Copy.PasswordFile == "" {
		add("copy", LevelFail, "copy.password_file is empty")
		return
	}
	if _, err := os.Stat(cfg.Copy.PasswordFile); err != nil {
		add("copy", LevelFail, err.Error())
		return
	}
	add("copy", LevelOK, "destination and password file are configured")
}

func checkRetention(cfg *config.Config, add func(string, Level, string)) {
	if !cfg.HasRetention() {
		add("retention", LevelWarn, "no retention policy, snapshots will grow forever")
		return
	}
	add("retention", LevelOK, strings.Join(cfg.RetentionArgs(), " "))
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
