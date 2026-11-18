// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Command jealous-vault backs up Linux and Proxmox hosts to restic
// repositories and makes restoring them a single command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mnnoee/jealous-vault/internal/audit"
	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/doctor"
	"github.com/mnnoee/jealous-vault/internal/freshness"
	"github.com/mnnoee/jealous-vault/internal/hooks"
	"github.com/mnnoee/jealous-vault/internal/lock"
	"github.com/mnnoee/jealous-vault/internal/notify"
	"github.com/mnnoee/jealous-vault/internal/proxmox"
	"github.com/mnnoee/jealous-vault/internal/recovery"
	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/restore"
	"github.com/mnnoee/jealous-vault/internal/runner"
	"github.com/mnnoee/jealous-vault/internal/schedule"
	"github.com/mnnoee/jealous-vault/internal/service"
	"github.com/mnnoee/jealous-vault/internal/sftp"
	"github.com/mnnoee/jealous-vault/internal/state"
	"github.com/mnnoee/jealous-vault/internal/systemd"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: %v\n", err)
		os.Exit(exitCode(err))
	}
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

func run(ctx context.Context, args []string) error {
	root := newRootCommand(ctx)
	root.SetArgs(args)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	return root.Execute()
}

func usage(w io.Writer) {
	fmt.Fprint(w, `JealousVault, encrypted backups and predictable restores.

Usage:
  jealous-vault <command> [flags]

Commands:
  init          write a config, generate the password and init the repository
  repo          init, unlock, stats, diff, delete, copy, passwd, key
  install       install the binary, systemd services and timers
  uninstall     stop the timers and remove the unit files
  backup        run a backup now
  vzdump        dump Proxmox VMs and back the archives up
  restore-vm    restore a VM or CT archive from a snapshot
  snapshots     list snapshots in the repository
  mount         mount a snapshot read-only through FUSE
  restore       restore a snapshot or the latest one
  test-restore  restore into a separate directory
  check         verify repository integrity
  doctor        check restic, config, repository, paths and timers
  freshness     check the backup age and the backup timer
  status        show the last run and whether the backup is fresh
  history       show recent backup, check and verify runs
  recovery-kit  write an offline recovery kit for this repository
  version       print version information

Run "jealous-vault <command> --help" for command flags.
`)
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func stringFlag(fs *flag.FlagSet, name, def, usage string) *string {
	return fs.String(name, def, usage)
}

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// recordAudit appends one critical action to the audit journal. A broken
// journal is reported but never changes the outcome of the operation itself.
func recordAudit(cfg *config.Config, log *slog.Logger, entry audit.Entry) {
	entry.Host = cfg.Host
	audit.Logger{Path: cfg.AuditFile, Log: log}.Record(entry)
}

func auditOutcome(err error) (result, message string) {
	if err == nil {
		return "success", ""
	}
	return "failed", err.Error()
}

func resticClient(cfg *config.Config, log *slog.Logger) *restic.Client {
	return restic.New(restic.Options{
		Binary:       cfg.ResticBinary,
		Repository:   cfg.Repository.URL,
		PasswordFile: cfg.Repository.PasswordFile,
		Env:          cfg.Repository.Env,
		CacheDir:     cfg.ResticCacheDir(),
		Extra:        cfg.ResticExtraOptions(),
		Logger:       log,
		Output:       os.Stdout,
	})
}

// prepare checks secret file permissions and the SFTP configuration before any
// command starts a restic process.
func prepare(cfg *config.Config) error {
	if err := cfg.CheckSecretFiles(); err != nil {
		return err
	}
	if err := sftp.EnsureConfig(cfg); err != nil {
		return err
	}
	// Best effort: the journal writer reports its own errors, and a read-only
	// filesystem must not stop a restore.
	if cfg.AuditFile != "" {
		_ = os.MkdirAll(filepath.Dir(cfg.AuditFile), 0o750)
	}
	return nil
}

// acquireRunLock returns busy=true when another backup, check or restore is
// already running. Busy is not an error, the caller exits successfully.
func acquireRunLock(cfg *config.Config, command string) (runLock *lock.Lock, busy bool, err error) {
	runLock, err = lock.Acquire(cfg.LockFile, lock.Info{
		PID:       os.Getpid(),
		Command:   command,
		StartedAt: time.Now(),
	})
	if err == nil {
		return runLock, false, nil
	}
	var busyErr *lock.BusyError
	if errors.As(err, &busyErr) {
		fmt.Fprintf(os.Stderr, "jealous-vault: %v, skip\n", busyErr)
		return nil, true, nil
	}
	return nil, false, err
}

func cmdInit(ctx context.Context, args []string) error {
	fs := newFlagSet("init")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file to write")
	host := stringFlag(fs, "host", "", "host name recorded in snapshots, defaults to the system hostname")
	job := stringFlag(fs, "job", "default", "job name, used in unit and state paths")
	repoURL := stringFlag(fs, "repo", "", "restic repository URL")
	paths := stringFlag(fs, "paths", "", "comma separated absolute paths to back up")
	excludes := stringFlag(fs, "excludes", "", "comma separated exclude patterns")
	profile := stringFlag(fs, "profile", "host", "backup profile: host, vm-dumps or custom")
	passwordFile := stringFlag(fs, "password-file", "", "file that will hold the repository password")
	cacheDir := stringFlag(fs, "cache-dir", config.DefaultCacheDir, "directory for the restic index cache")
	stateFile := stringFlag(fs, "state-file", "", "file that will hold the last run state, defaults per job")
	sftpKey := stringFlag(fs, "sftp-key", "", "private key for sftp repositories")
	sftpKnownHosts := stringFlag(fs, "sftp-known-hosts", "", "known_hosts file for sftp repositories")
	proxmox := fs.Bool("proxmox", false, "enable the Proxmox vzdump timer")
	force := fs.Bool("force", false, "overwrite an existing configuration")
	noRepoInit := fs.Bool("no-repo-init", false, "only write the config and password")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *repoURL == "" && !*noRepoInit {
		return errors.New("pass --repo to initialize the repository, or --no-repo-init to only write the config")
	}
	switch *profile {
	case "host", "vm-dumps", "custom":
	default:
		return fmt.Errorf("unknown profile %q, use host, vm-dumps or custom", *profile)
	}

	pwPath, err := writeInitialConfig(*configPath, config.InitOptions{
		Host:           *host,
		Job:            *job,
		RepositoryURL:  *repoURL,
		PasswordFile:   *passwordFile,
		CacheDir:       *cacheDir,
		StateFile:      *stateFile,
		Profile:        *profile,
		Paths:          splitComma(*paths),
		Excludes:       splitComma(*excludes),
		SFTPKeyFile:    *sftpKey,
		SFTPKnownHosts: *sftpKnownHosts,
		Proxmox:        *proxmox,
	}, *force)
	if err != nil {
		return err
	}

	fmt.Printf("config:   %s\n", *configPath)
	fmt.Printf("password: %s\n", pwPath)
	if *noRepoInit {
		fmt.Println()
		fmt.Println("Repository not initialized. Edit the config, then run:")
		fmt.Println("  jealous-vault repo init")
		return nil
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := initRepository(ctx, cfg, newLogger(*verbose)); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  jealous-vault install")
	fmt.Println("  jealous-vault backup")
	return nil
}

func cmdRepo(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jealous-vault repo <init|unlock|stats|diff|delete|copy|passwd|key> [--config path]")
	}
	switch args[0] {
	case "init":
		return cmdRepoInit(ctx, args[1:])
	case "unlock":
		return cmdRepoUnlock(ctx, args[1:])
	case "stats":
		return cmdRepoStats(ctx, args[1:])
	case "diff":
		return cmdRepoDiff(ctx, args[1:])
	case "delete":
		return cmdRepoDelete(ctx, args[1:])
	case "copy":
		return cmdRepoCopy(ctx, args[1:])
	case "passwd":
		return cmdRepoPasswd(ctx, args[1:])
	case "key":
		return cmdRepoKey(ctx, args[1:])
	default:
		return fmt.Errorf("unknown repo command %q", args[0])
	}
}

func cmdRepoInit(ctx context.Context, args []string) error {
	fs := newFlagSet("repo init")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "repo init")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()
	log := newLogger(*verbose)
	err = initRepository(ctx, cfg, log)
	result, message := auditOutcome(err)
	recordAudit(cfg, log, audit.Entry{Action: "repo.init", Result: result, Error: message})
	return err
}

func cmdRepoUnlock(ctx context.Context, args []string) error {
	fs := newFlagSet("repo unlock")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "repo unlock")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()
	log := newLogger(*verbose)
	err = resticClient(cfg, log).Unlock(ctx)
	result, message := auditOutcome(err)
	recordAudit(cfg, log, audit.Entry{Action: "repo.unlock", Result: result, Error: message})
	if err != nil {
		return err
	}
	fmt.Println("stale locks removed")
	return nil
}

func cmdRepoStats(ctx context.Context, args []string) error {
	fs := newFlagSet("repo stats")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	mode := stringFlag(fs, "mode", "restore-size", "restore-size, files-by-contents, raw-data or blobs-per-file")
	asJSON := fs.Bool("json", false, "print stats as JSON")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	switch *mode {
	case "restore-size", "files-by-contents", "raw-data", "blobs-per-file":
	default:
		return fmt.Errorf("unknown stats mode %q", *mode)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	stats, err := resticClient(cfg, newLogger(*verbose)).Stats(ctx, *mode)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}
	fmt.Printf("snapshots: %d\n", stats.SnapshotsCount)
	fmt.Printf("files:     %d\n", stats.TotalFileCount)
	fmt.Printf("blobs:     %d\n", stats.TotalBlobCount)
	fmt.Printf("size:      %s\n", humanBytes(stats.TotalSize))
	return nil
}

func initRepository(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := prepare(cfg); err != nil {
		return err
	}
	if err := resticClient(cfg, log).Init(ctx); err != nil {
		if alreadyInitialized(err) {
			fmt.Println("repository already initialized")
			return nil
		}
		return err
	}
	fmt.Println("repository initialized")
	return nil
}

// writeInitialConfig creates the config and a password file when they do not
// exist yet. It returns the password path.
func writeInitialConfig(path string, opts config.InitOptions, force bool) (string, error) {
	if opts.PasswordFile == "" {
		passwordPath, err := filepath.Abs(filepath.Join(filepath.Dir(path), "restic.pass"))
		if err != nil {
			return "", err
		}
		opts.PasswordFile = passwordPath
	}
	if err := config.WriteExample(path, opts, force); err != nil {
		return "", err
	}
	if _, err := os.Stat(opts.PasswordFile); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if err := config.GeneratePassword(opts.PasswordFile); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(opts.PasswordFile, 0o600); err != nil {
		return "", err
	}
	return opts.PasswordFile, nil
}

func cmdInstall(ctx context.Context, args []string) error {
	fs := newFlagSet("install")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	prefix := stringFlag(fs, "prefix", "/usr/local/bin", "directory for the binary")
	initSystem := stringFlag(fs, "init", "", "scheduler: systemd, openrc or cron, detected when empty")
	unitDir := stringFlag(fs, "unit-dir", "", "override the systemd unit directory")
	noStart := fs.Bool("no-start", false, "write units but do not start the timers")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	if _, err := os.Stat(*configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s not found, run \"jealous-vault init\" first", *configPath)
		}
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	log := newLogger(*verbose)
	if _, lookErr := exec.LookPath(cfg.ResticBinary); lookErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %s not found in PATH, install restic before the first backup\n", cfg.ResticBinary)
	}

	installer := &systemd.Installer{
		BinaryPath: filepath.Join(*prefix, "jealous-vault"),
		ConfigPath: *configPath,
		Job:        cfg.Job,
		Init:       *initSystem,
		UnitDir:    *unitDir,
		SkipStart:  *noStart,
		Out:        os.Stdout,
		Log:        log,
	}
	if err := installer.Install(ctx, cfg); err != nil {
		result, message := auditOutcome(err)
		recordAudit(cfg, log, audit.Entry{Action: "install", Result: result, Error: message})
		return err
	}
	recordAudit(cfg, log, audit.Entry{Action: "install", Result: "success"})

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  jealous-vault backup")
	fmt.Println("  jealous-vault snapshots")
	return nil
}

func alreadyInitialized(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "already initialized") || strings.Contains(text, "already exists")
}

func cmdUninstall(ctx context.Context, args []string) error {
	fs := newFlagSet("uninstall")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	prefix := stringFlag(fs, "prefix", "/usr/local/bin", "directory of the binary")
	job := stringFlag(fs, "job", "", "job name, defaults to the config value")
	initSystem := stringFlag(fs, "init", "", "scheduler: systemd, openrc or cron, detected when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *job == "" {
		if cfg, err := config.Load(*configPath); err == nil {
			*job = cfg.Job
		} else {
			*job = "default"
		}
	}

	installer := &systemd.Installer{
		BinaryPath: filepath.Join(*prefix, "jealous-vault"),
		ConfigPath: *configPath,
		Job:        *job,
		Init:       *initSystem,
		Out:        os.Stdout,
	}
	err := installer.Uninstall(ctx)
	if cfg, loadErr := config.Load(*configPath); loadErr == nil {
		result, message := auditOutcome(err)
		recordAudit(cfg, newLogger(false), audit.Entry{Action: "uninstall", Result: result, Error: message})
	}
	return err
}

func cmdBackup(ctx context.Context, args []string) error {
	fs := newFlagSet("backup")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print the run result as JSON")
	dryRun := fs.Bool("dry-run", false, "show what would be backed up without writing")
	noPrune := fs.Bool("no-prune", false, "skip repository pruning after retention")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "backup")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()
	if *noPrune {
		cfg.Retention.Prune = false
	}

	log := newLogger(*verbose)
	r := runner.New(cfg, log, os.Stderr)
	if *dryRun {
		summary, err := r.DryRun(ctx)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if encErr := enc.Encode(summary); encErr != nil && err == nil {
				err = encErr
			}
			return err
		}
		fmt.Println("backup dry-run")
		fmt.Printf("  files:    %d new, %d changed\n", summary.FilesNew, summary.FilesChanged)
		fmt.Printf("  added:    %s\n", humanBytes(summary.DataAdded))
		fmt.Printf("  scanned:  %s\n", humanBytes(summary.TotalBytesProcessed))
		return err
	}
	run, err := r.Backup(ctx)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(run); encErr != nil && err == nil {
			err = encErr
		}
	} else {
		printRun(run)
	}
	return err
}

func printRun(run state.Run) {
	fmt.Printf("%s %s\n", run.Kind, run.Status)
	if run.SnapshotID != "" {
		fmt.Printf("  snapshot: %s\n", run.SnapshotID)
	}
	if run.FilesNew > 0 || run.FilesChanged > 0 {
		fmt.Printf("  files:    %d new, %d changed\n", run.FilesNew, run.FilesChanged)
	}
	if run.BytesAdded > 0 {
		fmt.Printf("  added:    %s\n", humanBytes(run.BytesAdded))
	}
	fmt.Printf("  duration: %s\n", time.Duration(run.DurationSeconds*float64(time.Second)).Round(time.Second))
	for _, w := range run.Warnings {
		fmt.Printf("  warning:  %s\n", w)
	}
	if run.Error != "" {
		fmt.Printf("  error:    %s\n", run.Error)
	}
}

func cmdVzdump(ctx context.Context, args []string) error {
	fs := newFlagSet("vzdump")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	all := fs.Bool("all", false, "dump every VM and CT")
	mode := stringFlag(fs, "mode", "", "override proxmox.mode")
	compress := stringFlag(fs, "compress", "", "override proxmox.compress")
	keep := fs.Int("keep", -1, "override proxmox.keep_last")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "vzdump")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	manager := proxmox.New(proxmox.Options{
		DumpDir:  cfg.Proxmox.DumpDir,
		Mode:     firstNonEmpty(*mode, cfg.Proxmox.Mode),
		Compress: firstNonEmpty(*compress, cfg.Proxmox.Compress),
		Storage:  cfg.Proxmox.Storage,
		Logger:   log,
	})
	ids, err := selectGuestIDs(ctx, cfg, fs.Args(), *all, manager)
	if err != nil {
		return err
	}

	var archives []string
	for _, id := range ids {
		fmt.Printf("dumping %d\n", id)
		archive, err := manager.Dump(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("  archive: %s\n", archive)
		archives = append(archives, archive)
	}

	backupCfg := *cfg
	backupCfg.Backup.Paths = archives
	backupCfg.Backup.Excludes = nil
	backupCfg.Backup.PreHooks = nil
	backupCfg.Backup.PostHooks = nil
	backupCfg.Backup.Profile = "custom"
	backupCfg.Backup.Dumps = config.DumpOptions{}
	backupCfg.Backup.Tags = append(append([]string(nil), cfg.Backup.Tags...), "vzdump")
	for _, id := range ids {
		backupCfg.Backup.Tags = append(backupCfg.Backup.Tags, fmt.Sprintf("vmid-%d", id))
	}

	run, err := runner.New(&backupCfg, log, os.Stderr).Backup(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: dump backup failed, archives kept in %s\n", cfg.Proxmox.DumpDir)
		return err
	}
	fmt.Printf("backup %s, snapshot %s\n", run.Status, run.SnapshotID)

	keepLast := cfg.Proxmox.KeepLast
	if *keep >= 0 {
		keepLast = *keep
	}
	if err := manager.Prune(keepLast); err != nil {
		return err
	}
	fmt.Printf("kept last %d dump(s) per guest\n", keepLast)
	return nil
}

func cmdRestoreVM(ctx context.Context, args []string) error {
	fs := newFlagSet("restore-vm")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	storage := stringFlag(fs, "storage", "", "override proxmox.storage")
	force := fs.Bool("force", false, "overwrite an existing VM or CT")
	targetFlag := stringFlag(fs, "target", "", "directory for the temporary archive restore")
	keep := fs.Bool("keep", false, "keep the temporary archive directory")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: jealous-vault restore-vm <vmid> <snapshot|latest>")
	}
	id, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid vmid %q", fs.Arg(0))
	}
	snapshotRef := fs.Arg(1)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "restore-vm")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	client := resticClient(cfg, log)
	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	snapshot, err := resolveSnapshot(snapshots, snapshotRef, cfg.Host)
	if err != nil {
		return err
	}

	autoTarget := *targetFlag == ""
	target := *targetFlag
	if autoTarget {
		target = filepath.Join(filepath.Dir(cfg.StateFile), "restore-vm", time.Now().UTC().Format("20060102-150405"))
	}
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}

	plan, err := restore.New(snapshot.ID, target, []string{cfg.Proxmox.DumpDir}, restore.OverwriteAlways)
	if err != nil {
		return err
	}
	plan.Host = cfg.Host
	fmt.Printf("restoring dump archive from %s into %s\n", snapshot.ShortID, target)
	if err := plan.Execute(ctx, client); err != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: archive kept in %s\n", target)
		return err
	}

	restoredDir := filepath.Join(target, strings.TrimPrefix(cfg.Proxmox.DumpDir, "/"))
	manager := proxmox.New(proxmox.Options{
		DumpDir: restoredDir,
		Storage: firstNonEmpty(*storage, cfg.Proxmox.Storage),
		Logger:  log,
	})
	fmt.Printf("restoring guest %d with storage %s\n", id, firstNonEmpty(*storage, cfg.Proxmox.Storage))
	if err := manager.RestoreFromDir(ctx, id, restoredDir, *force); err != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: archive kept in %s\n", target)
		return err
	}

	if autoTarget && !*keep {
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(os.Stderr, "jealous-vault: cannot clean up %s: %v\n", target, err)
		}
	}
	fmt.Printf("guest %d restored from snapshot %s\n", id, snapshot.ShortID)
	return nil
}

func selectGuestIDs(ctx context.Context, cfg *config.Config, raw []string, all bool, manager *proxmox.Manager) ([]int, error) {
	if len(raw) > 0 {
		return parseIDs(raw)
	}
	if len(cfg.Proxmox.VMs) > 0 {
		return cfg.Proxmox.VMs, nil
	}
	if all || cfg.Proxmox.All {
		return manager.Guests(ctx)
	}
	return nil, errors.New("no VMIDs: pass IDs, use --all, or set proxmox.vms")
}

func parseIDs(args []string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid vmid %q", arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cmdRepoDiff(ctx context.Context, args []string) error {
	fs := newFlagSet("repo diff")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print restic diff JSON")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: jealous-vault repo diff <snapshot> <snapshot>")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	client := resticClient(cfg, newLogger(*verbose))
	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	first, err := resolveSnapshot(snapshots, fs.Arg(0), "")
	if err != nil {
		return err
	}
	second, err := resolveSnapshot(snapshots, fs.Arg(1), "")
	if err != nil {
		return err
	}
	return client.Diff(ctx, first.ID, second.ID, *asJSON)
}

func cmdRepoDelete(ctx context.Context, args []string) error {
	fs := newFlagSet("repo delete")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	yes := fs.Bool("yes", false, "confirm snapshot deletion")
	prune := fs.Bool("prune", false, "prune unreferenced data after deletion")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: jealous-vault repo delete --yes <snapshot>")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "repo delete")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	client := resticClient(cfg, log)
	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	snapshot, err := resolveSnapshot(snapshots, fs.Arg(0), "")
	if err != nil {
		return err
	}
	fmt.Printf("snapshot: %s %s %s\n", snapshot.ShortID, snapshot.Time.Local().Format("2006-01-02 15:04"), snapshot.Hostname)
	if !*yes {
		recordAudit(cfg, log, audit.Entry{
			Action:   "repo.delete",
			Snapshot: snapshot.ID,
			Result:   "refused",
			Error:    "missing --yes",
		})
		return errors.New("refusing to delete a snapshot without --yes")
	}
	err = client.ForgetSnapshot(ctx, snapshot.ID, *prune)
	result, message := auditOutcome(err)
	recordAudit(cfg, log, audit.Entry{
		Action:   "repo.delete",
		Snapshot: snapshot.ID,
		Result:   result,
		Error:    message,
	})
	if err != nil {
		return err
	}
	if *prune {
		fmt.Println("snapshot deleted and repository pruned")
	} else {
		fmt.Println("snapshot deleted, data remains until the next prune")
	}
	return nil
}

func cmdRepoCopy(ctx context.Context, args []string) error {
	fs := newFlagSet("repo copy")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	all := fs.Bool("all", false, "copy every snapshot instead of only missing ones")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Copy.Repository == "" {
		return errors.New("copy.repository is not configured")
	}
	if cfg.Copy.PasswordFile == cfg.Repository.PasswordFile {
		return errors.New("copy.password_file must differ from the primary repository password")
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "repo copy")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	newOnly := cfg.Copy.NewOnly && !*all
	dest := restic.CopyDest{
		Repository:   cfg.Copy.Repository,
		PasswordFile: cfg.Copy.PasswordFile,
		Env:          cfg.Copy.Env,
		CacheDir:     cfg.ResticCacheDir(),
	}
	fmt.Printf("copying to %s, new only: %v\n", config.RedactURL(cfg.Copy.Repository), newOnly)
	err = resticClient(cfg, log).CopyTo(ctx, dest, fs.Args(), newOnly)

	notifier := notify.New(notify.Options{
		Webhooks:        cfg.Notify.Webhooks,
		DedupFile:       cfg.AlertsFile,
		DedupBackup:     cfg.DedupBackup,
		DedupFreshness:  cfg.DedupFreshness,
		Email:           notify.EmailOptionsFromConfig(cfg),
		TelegramToken:   cfg.Notify.TelegramToken,
		TelegramChatID:  cfg.Notify.TelegramChatID,
		TelegramChatIDs: cfg.Notify.TelegramChatIDs,
		NotifyOnSuccess: cfg.Notify.NotifyOnSuccess,
		Timeout:         cfg.NotifyTimeout,
		Logger:          log,
	})
	event := notify.Event{Event: "copy", Status: "success", Host: cfg.Host, Time: time.Now()}
	if err != nil {
		event.Status = "failed"
		event.Error = err.Error()
	}
	if notifyErr := notifier.Send(ctx, event); notifyErr != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: cannot send copy alert: %v\n", notifyErr)
	}
	copyResult, copyError := auditOutcome(err)
	recordAudit(cfg, log, audit.Entry{
		Action: "repo.copy",
		Target: config.RedactURL(cfg.Copy.Repository),
		Result: copyResult,
		Error:  copyError,
	})
	if err != nil {
		return err
	}
	fmt.Println("copy finished")
	return nil
}

func cmdRepoPasswd(ctx context.Context, args []string) error {
	fs := newFlagSet("repo passwd")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	newPassword := stringFlag(fs, "new-password-file", "", "file for the new repository password")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *newPassword == "" {
		return errors.New("--new-password-file is required")
	}
	if !filepath.IsAbs(*newPassword) {
		return fmt.Errorf("new password file %q must be absolute", *newPassword)
	}
	if _, err := os.Stat(*newPassword); err == nil {
		return fmt.Errorf("%s already exists", *newPassword)
	} else if !os.IsNotExist(err) {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "repo passwd")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	if err := config.GeneratePassword(*newPassword); err != nil {
		return fmt.Errorf("generate new password: %w", err)
	}
	log := newLogger(*verbose)
	client := resticClient(cfg, log)
	if err := client.AddKey(ctx, *newPassword); err != nil {
		return fmt.Errorf("add new key: %w", err)
	}

	verifyEnv := make(map[string]string, len(cfg.Repository.Env))
	for key, value := range cfg.Repository.Env {
		switch key {
		case "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND":
			continue
		}
		verifyEnv[key] = value
	}
	verify := restic.New(restic.Options{
		Binary:       cfg.ResticBinary,
		Repository:   cfg.Repository.URL,
		PasswordFile: *newPassword,
		Env:          verifyEnv,
		CacheDir:     cfg.ResticCacheDir(),
		Extra:        cfg.ResticExtraOptions(),
		Logger:       log,
		Output:       io.Discard,
	})
	if err := verify.CheckAccess(ctx); err != nil {
		return fmt.Errorf("new key was added, but verification failed; remove it with repo key list: %w", err)
	}

	fmt.Printf("new key: %s\n", *newPassword)
	fmt.Println("Next steps:")
	fmt.Printf("  set repository.password_file = %q in %s\n", *newPassword, *configPath)
	fmt.Println("  jealous-vault repo key list")
	fmt.Println("  jealous-vault repo key remove <old-id> --yes")
	fmt.Println("Keep the old key until the new password is verified in production.")
	return nil
}

func cmdRepoKey(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jealous-vault repo key <list|remove> [flags]")
	}
	switch args[0] {
	case "list":
		fs := newFlagSet("repo key list")
		configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
		verbose := fs.Bool("verbose", false, "debug logging")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		if err := prepare(cfg); err != nil {
			return err
		}
		return resticClient(cfg, newLogger(*verbose)).Keys(ctx)
	case "remove":
		fs := newFlagSet("repo key remove")
		configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
		yes := fs.Bool("yes", false, "confirm key removal")
		verbose := fs.Bool("verbose", false, "debug logging")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: jealous-vault repo key remove --yes <key-id>")
		}
		if !*yes {
			return errors.New("refusing to remove a repository key without --yes")
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		if err := prepare(cfg); err != nil {
			return err
		}
		runLock, busy, err := acquireRunLock(cfg, "repo key remove")
		if err != nil {
			return err
		}
		if busy {
			return nil
		}
		defer runLock.Release()
		return resticClient(cfg, newLogger(*verbose)).RemoveKey(ctx, fs.Arg(0))
	default:
		return fmt.Errorf("unknown repo key command %q", args[0])
	}
}

func cmdSnapshots(ctx context.Context, args []string) error {
	fs := newFlagSet("snapshots")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print snapshots as JSON")
	allHosts := fs.Bool("all", false, "list snapshots from every host")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	client := resticClient(cfg, newLogger(*verbose))
	host := cfg.Host
	if *allHosts {
		host = ""
	}
	snapshots, err := client.Snapshots(ctx, host)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snapshots)
	}
	if len(snapshots) == 0 {
		fmt.Println("no snapshots")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTIME\tHOST\tTAGS\tPATHS")
	for _, s := range snapshots {
		id := s.ShortID
		if id == "" {
			id = s.ID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			id,
			s.Time.Local().Format("2006-01-02 15:04"),
			s.Hostname,
			strings.Join(s.Tags, ","),
			truncate(strings.Join(s.Paths, ","), 60),
		)
	}
	return w.Flush()
}

func cmdMount(ctx context.Context, args []string) error {
	fs := newFlagSet("mount")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: jealous-vault mount <snapshot|latest> <mountpoint>")
	}
	snapshotRef := fs.Arg(0)
	target := fs.Arg(1)
	if !filepath.IsAbs(target) {
		return fmt.Errorf("mountpoint %q must be an absolute path", target)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	log := newLogger(*verbose)
	client := resticClient(cfg, log)
	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	snapshot, err := resolveSnapshot(snapshots, snapshotRef, cfg.Host)
	if err != nil {
		return err
	}

	info, err := os.Stat(target)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(target, 0o750); err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("mountpoint %q is not a directory", target)
	}

	fmt.Printf("mounting repository at %s\n", target)
	displayID := snapshot.ShortID
	if displayID == "" {
		displayID = snapshot.ID
	}
	fmt.Printf("snapshot %s is available at %s/ids/%s\n", displayID, target, displayID)
	fmt.Println("press Ctrl-C to unmount")
	err = client.Mount(ctx, target, os.Stdout, os.Stderr)
	result, message := auditOutcome(err)
	if errors.Is(err, context.Canceled) {
		result, message = "interrupted", "unmounted by signal"
	}
	recordAudit(cfg, log, audit.Entry{
		Action:   "mount",
		Snapshot: snapshot.ID,
		Target:   target,
		Result:   result,
		Error:    message,
	})
	return err
}

func resolveSnapshot(snapshots []restic.Snapshot, ref, host string) (restic.Snapshot, error) {
	var candidates []restic.Snapshot
	for _, s := range snapshots {
		if host == "" || s.Hostname == host {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return restic.Snapshot{}, fmt.Errorf("no snapshots for host %s", host)
	}

	if ref == "" || ref == "latest" {
		newest := candidates[0]
		for _, s := range candidates[1:] {
			if s.Time.After(newest.Time) {
				newest = s
			}
		}
		return newest, nil
	}

	needle := strings.ToLower(ref)
	for _, s := range candidates {
		id := strings.ToLower(s.ID)
		short := strings.ToLower(s.ShortID)
		if id == needle || short == needle || strings.HasPrefix(id, needle) {
			return s, nil
		}
	}
	return restic.Snapshot{}, fmt.Errorf("snapshot %q not found", ref)
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func cmdRestore(ctx context.Context, args []string) error {
	fs := newFlagSet("restore")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	snapshot := stringFlag(fs, "snapshot", "latest", "snapshot ID or latest")
	target := stringFlag(fs, "target", "/", "directory to restore into")
	overwrite := stringFlag(fs, "overwrite", restore.OverwriteAlways, "always, never or if-changed")
	verify := fs.Bool("verify", false, "verify restored files against the repository")
	estimate := fs.Bool("estimate", false, "print the restore size without writing anything")
	recipe := stringFlag(fs, "recipe", "", "named restore recipe from the config")
	yes := fs.Bool("yes", false, "confirm a restore into /")
	asJSON := fs.Bool("json", false, "print the estimate as JSON")
	var includes stringList
	fs.Var(&includes, "include", "path inside the snapshot to restore, repeatable")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if *recipe != "" {
		for _, name := range []string{"target", "include", "overwrite", "verify"} {
			if explicit[name] {
				return fmt.Errorf("--recipe cannot be combined with --%s", name)
			}
		}
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	log := newLogger(*verbose)
	client := resticClient(cfg, log)

	if *estimate {
		snapshots, err := client.Snapshots(ctx, "")
		if err != nil {
			return err
		}
		snapshotInfo, err := resolveSnapshot(snapshots, *snapshot, cfg.Host)
		if err != nil {
			return err
		}
		stats, err := client.StatsFor(ctx, snapshotInfo.ID, "restore-size")
		if err != nil {
			return err
		}
		out := struct {
			Snapshot string `json:"snapshot"`
			Files    int64  `json:"files"`
			Bytes    int64  `json:"bytes"`
		}{Snapshot: snapshotInfo.ID, Files: stats.TotalFileCount, Bytes: stats.TotalSize}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}
		fmt.Printf("snapshot: %s\n", snapshotInfo.ShortID)
		fmt.Printf("files:    %d\n", stats.TotalFileCount)
		fmt.Printf("size:     %s\n", humanBytes(stats.TotalSize))
		return nil
	}

	preHooks := append([]string(nil), cfg.Restore.PreHooks...)
	postHooks := append([]string(nil), cfg.Restore.PostHooks...)
	serviceName := ""

	var plan *restore.Plan
	if *recipe != "" {
		selected, ok := cfg.Restore.Recipes[*recipe]
		if !ok {
			return fmt.Errorf("restore recipe %q is not configured", *recipe)
		}
		plan, err = restore.New(*snapshot, selected.Target, selected.Includes, selected.Overwrite)
		if err != nil {
			return err
		}
		plan.Verify = selected.Verify
		if selected.PreHook != "" {
			preHooks = append(preHooks, selected.PreHook)
		}
		if selected.PostHook != "" {
			postHooks = append([]string{selected.PostHook}, postHooks...)
		}
		serviceName = selected.Service
	} else {
		plan, err = restore.New(*snapshot, *target, includes, *overwrite)
		if err != nil {
			return err
		}
		plan.Verify = *verify
	}

	runLock, busy, err := acquireRunLock(cfg, "restore")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()
	plan.Host = cfg.Host
	// Resolve latest to a concrete ID before the audit entry is written: the
	// journal must say exactly which snapshot was restored.
	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	resolved, err := resolveSnapshot(snapshots, plan.Snapshot, cfg.Host)
	if err != nil {
		return err
	}
	plan.Snapshot = resolved.ID
	fmt.Print(plan.Describe())
	if plan.Dangerous() && !*yes {
		return fmt.Errorf("%w, pass --yes", restore.ErrDangerous)
	}
	fmt.Println("restoring")

	if err := executeRestore(ctx, cfg, plan, log, "restore", preHooks, postHooks, serviceName); err != nil {
		return err
	}
	fmt.Println("restore finished")
	return nil
}

// executeRestore runs restore hooks and an optional service stop/start around
// the plan. A recipe can therefore stop PostgreSQL, restore the directory and
// start PostgreSQL again without shelling out from the config.
func executeRestore(ctx context.Context, cfg *config.Config, plan *restore.Plan, log *slog.Logger, event string, preHooks, postHooks []string, serviceName string) error {
	env := map[string]string{
		"JM_EVENT":    event,
		"JM_HOST":     cfg.Host,
		"JM_SNAPSHOT": plan.Snapshot,
		"JM_TARGET":   plan.Target,
		"JM_STATUS":   "running",
		"JM_PHASE":    "pre",
	}
	stopped := false
	if serviceName != "" {
		if err := service.Run(ctx, log, "stop", serviceName); err != nil {
			return err
		}
		stopped = true
	}
	finish := func(cause error) error {
		env["JM_PHASE"] = "post"
		if cause == nil {
			env["JM_STATUS"] = "success"
		} else {
			env["JM_STATUS"] = "failed"
		}
		hookErr := hooks.Run(ctx, log, event+"-post", postHooks, env)
		var startErr error
		if stopped {
			startErr = service.Run(context.Background(), log, "start", serviceName)
		}
		finalErr := errors.Join(cause, hookErr, startErr)
		result, message := auditOutcome(finalErr)
		recordAudit(cfg, log, audit.Entry{
			Action:   event,
			Snapshot: plan.Snapshot,
			Target:   plan.Target,
			Result:   result,
			Error:    message,
		})
		return finalErr
	}
	if err := hooks.Run(ctx, log, event+"-pre", preHooks, env); err != nil {
		return finish(err)
	}

	restoreErr := plan.Execute(ctx, resticClient(cfg, log))
	return finish(restoreErr)
}

func cmdTestRestore(ctx context.Context, args []string) error {
	fs := newFlagSet("test-restore")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	snapshotRef := stringFlag(fs, "snapshot", "latest", "snapshot ID or latest")
	targetFlag := stringFlag(fs, "target", "", "directory for the test restore")
	levelFlag := stringFlag(fs, "level", "", "files or service, defaults to verify.level")
	maxSizeFlag := stringFlag(fs, "max-size", "", "override verify.max_size")
	minFreeFlag := stringFlag(fs, "min-free", "", "override verify.min_free")
	postHookFlag := stringFlag(fs, "post-hook", "", "override verify.post_hook")
	timeoutFlag := fs.Duration("timeout", 0, "override verify.timeout")
	keep := fs.Bool("keep", false, "keep an automatic target directory on success")
	noNotify := fs.Bool("no-notify", false, "do not send alerts")
	yes := fs.Bool("yes", false, "confirm a restore into a system directory")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "test-restore")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	level := cfg.Verify.Level
	if *levelFlag != "" {
		level = *levelFlag
	}
	if level != "files" && level != "service" {
		return fmt.Errorf("unknown verify level %q, use files or service", level)
	}
	postHook := cfg.Verify.PostHook
	if *postHookFlag != "" {
		postHook = *postHookFlag
	}
	if level == "service" && postHook == "" {
		return errors.New("verify.post_hook is required for the service level")
	}

	maxSize := cfg.VerifyMaxSize
	if *maxSizeFlag != "" {
		maxSize, err = config.ParseSize(*maxSizeFlag)
		if err != nil {
			return err
		}
	}
	minFree := cfg.VerifyMinFree
	if *minFreeFlag != "" {
		minFree, err = config.ParseSize(*minFreeFlag)
		if err != nil {
			return err
		}
	}
	timeout := cfg.VerifyTimeout
	if *timeoutFlag > 0 {
		timeout = *timeoutFlag
	}

	now := time.Now()
	autoTarget := *targetFlag == ""
	target := *targetFlag
	if autoTarget {
		target = restore.TestTarget(filepath.Join(filepath.Dir(cfg.StateFile), "verify"), now)
	}
	log := newLogger(*verbose)
	client := resticClient(cfg, log)
	store := state.Store{Path: cfg.StateFile, HistoryPath: cfg.HistoryFile}
	run := state.Run{Kind: state.KindVerify, StartedAt: now, Status: state.StatusFailed}
	var snapshotID string

	finish := func(status string, cause error) error {
		run.Status = status
		run.FinishedAt = time.Now()
		run.DurationSeconds = run.FinishedAt.Sub(run.StartedAt).Seconds()
		if cause != nil {
			run.Error = cause.Error()
		}
		if _, err := store.Record(run); err != nil {
			log.Error("cannot write state", "error", err)
		}
		recordAudit(cfg, log, audit.Entry{
			Time:     run.FinishedAt,
			Action:   "verify",
			Snapshot: snapshotID,
			Target:   target,
			Result:   status,
			Error:    run.Error,
		})
		if !*noNotify {
			notifier := notify.New(notify.Options{
				Webhooks:        cfg.Notify.Webhooks,
				DedupFile:       cfg.AlertsFile,
				DedupBackup:     cfg.DedupBackup,
				DedupFreshness:  cfg.DedupFreshness,
				Email:           notify.EmailOptionsFromConfig(cfg),
				TelegramToken:   cfg.Notify.TelegramToken,
				TelegramChatID:  cfg.Notify.TelegramChatID,
				TelegramChatIDs: cfg.Notify.TelegramChatIDs,
				NotifyOnSuccess: cfg.Notify.NotifyOnSuccess,
				Timeout:         cfg.NotifyTimeout,
				Logger:          log,
			})
			ev := notify.Event{
				Event:           "verify",
				Status:          status,
				Host:            cfg.Host,
				Snapshot:        snapshotID,
				DurationSeconds: run.DurationSeconds,
				Error:           run.Error,
				Time:            run.FinishedAt,
			}
			if err := notifier.Send(ctx, ev); err != nil {
				fmt.Fprintf(os.Stderr, "jealous-vault: cannot send verify alert: %v\n", err)
			}
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(run); err != nil {
				return err
			}
		} else {
			fmt.Printf("verify %s\n", status)
			if run.Error != "" {
				fmt.Printf("  %s\n", run.Error)
			}
			if _, err := os.Stat(target); err == nil {
				fmt.Printf("  target: %s\n", target)
			}
		}
		if status == state.StatusFailed {
			if cause != nil {
				return cause
			}
			return errors.New("verification failed")
		}
		return nil
	}

	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	snapshot, err := resolveSnapshot(snapshots, *snapshotRef, cfg.Host)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	snapshotID = snapshot.ID
	run.SnapshotID = snapshot.ID

	stats, err := client.StatsFor(ctx, snapshot.ID, "restore-size")
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	if reason := verifySkipReason(stats.TotalSize, 0, maxSize, 0); reason != "" {
		return finish(state.StatusSkipped, errors.New(reason))
	}

	if err := os.MkdirAll(target, 0o750); err != nil {
		return finish(state.StatusFailed, err)
	}
	free, err := freeBytes(target)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	if reason := verifySkipReason(0, free, 0, minFree); reason != "" {
		if autoTarget {
			os.RemoveAll(target)
		}
		return finish(state.StatusSkipped, errors.New(reason))
	}

	plan, err := restore.New(snapshot.ID, target, nil, restore.OverwriteAlways)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	plan.Host = cfg.Host
	plan.Verify = level == "files"
	safeRoot := filepath.Join(filepath.Dir(cfg.StateFile), "verify")
	if plan.Dangerous(safeRoot) && !*yes {
		return finish(state.StatusFailed, fmt.Errorf("%w, pass --yes", restore.ErrDangerous))
	}

	fmt.Printf("restoring %s into %s, level %s\n", snapshot.ShortID, target, level)
	restoreCtx, cancel := context.WithTimeout(ctx, timeout)
	err = plan.Execute(restoreCtx, client)
	cancel()
	if err != nil {
		return finish(state.StatusFailed, err)
	}

	if level == "service" {
		env := map[string]string{
			"JM_EVENT":         "verify",
			"JM_PHASE":         "post",
			"JM_STATUS":        "success",
			"JM_HOST":          cfg.Host,
			"JM_SNAPSHOT":      snapshot.ID,
			"JM_VERIFY_TARGET": target,
		}
		if err := hooks.Run(ctx, log, "verify", []string{postHook}, env); err != nil {
			return finish(state.StatusFailed, err)
		}
	}

	if autoTarget && !*keep {
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(os.Stderr, "jealous-vault: cannot clean up %s: %v\n", target, err)
		} else {
			fmt.Printf("cleaned up %s\n", target)
		}
	}
	return finish(state.StatusSuccess, nil)
}

func verifySkipReason(size, free, maxSize, minFree int64) string {
	if maxSize > 0 && size > maxSize {
		return fmt.Sprintf("snapshot is %s, limit %s", humanBytes(size), humanBytes(maxSize))
	}
	if minFree > 0 && free < minFree {
		return fmt.Sprintf("free space is %s, minimum %s", humanBytes(free), humanBytes(minFree))
	}
	return ""
}

func freeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// cmdTick is the portable scheduler entry point: cron or a small init loop
// wakes it up, it checks the schedules and the state, runs what is due and
// exits. No long-running process is needed.
func cmdTick(ctx context.Context, args []string) error {
	fs := newFlagSet("tick")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "tick")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	store := state.Store{Path: cfg.StateFile, HistoryPath: cfg.HistoryFile}
	st, err := store.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	var errs []error
	ran := false

	if due, at, err := schedule.Due(cfg.Schedule.OnCalendar, lastFinished(st.LastSuccess), now); err != nil {
		errs = append(errs, fmt.Errorf("backup schedule: %w", err))
	} else if due {
		ran = true
		fmt.Printf("tick: backup due since %s\n", at.Format(time.RFC3339))
		if _, err := runner.New(cfg, log, os.Stderr).Backup(ctx); err != nil {
			errs = append(errs, fmt.Errorf("backup: %w", err))
		}
	}

	if due, at, err := schedule.Due(systemd.CheckCalendar, lastFinished(st.LastCheck), now); err != nil {
		errs = append(errs, fmt.Errorf("check schedule: %w", err))
	} else if due {
		ran = true
		fmt.Printf("tick: check due since %s\n", at.Format(time.RFC3339))
		if _, err := runner.New(cfg, log, os.Stderr).Check(ctx); err != nil {
			errs = append(errs, fmt.Errorf("check: %w", err))
		}
	}

	// Freshness has no run history, so the last run is kept in a stamp file.
	stamp := filepath.Join(filepath.Dir(cfg.StateFile), "freshness.stamp")
	if due, at, err := schedule.Due(cfg.Freshness.Schedule, readStamp(stamp), now); err != nil {
		errs = append(errs, fmt.Errorf("freshness schedule: %w", err))
	} else if due {
		ran = true
		fmt.Printf("tick: freshness due since %s\n", at.Format(time.RFC3339))
		if err := cmdFreshness(ctx, []string{"--config", cfg.Path}); err != nil {
			errs = append(errs, fmt.Errorf("freshness: %w", err))
		} else if err := writeStamp(stamp, now); err != nil {
			errs = append(errs, err)
		}
	}

	if !ran {
		fmt.Println("tick: nothing due")
	}
	return errors.Join(errs...)
}

func lastFinished(run *state.Run) time.Time {
	if run == nil {
		return time.Time{}
	}
	return run.FinishedAt
}

func readStamp(path string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return stamp
}

func writeStamp(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(now.Format(time.RFC3339)+"\n"), 0o600)
}

func cmdCheck(ctx context.Context, args []string) error {
	fs := newFlagSet("check")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	readDataSubset := stringFlag(fs, "read-data-subset", "", "override check.read_data_subset, for example 5%")
	asJSON := fs.Bool("json", false, "print the run result as JSON")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "check")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()
	if *readDataSubset != "" {
		cfg.Check.ReadDataSubset = *readDataSubset
	}

	r := runner.New(cfg, newLogger(*verbose), os.Stderr)
	run, err := r.Check(ctx)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(run); encErr != nil && err == nil {
			err = encErr
		}
	} else {
		printRun(run)
	}
	return err
}

func cmdDoctor(ctx context.Context, args []string) error {
	fs := newFlagSet("doctor")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	sendTestEmail := fs.Bool("send-test-email", false, "send one test email and report the result")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Printf("[fail] config %v\n", err)
		return &exitError{code: 1, err: errors.New("configuration is not usable")}
	}
	if err := sftp.EnsureConfig(cfg); err != nil {
		fmt.Printf("[fail] sftp %v\n", err)
		return &exitError{code: 1, err: errors.New("sftp configuration is not usable")}
	}

	report := doctor.Run(ctx, cfg, doctor.Options{Logger: newLogger(*verbose)})
	if *sendTestEmail {
		result := doctor.Result{Name: "email test", Level: doctor.LevelOK, Detail: "test email sent"}
		if err := notify.SendTestEmail(ctx, notify.EmailOptionsFromConfig(cfg)); err != nil {
			result = doctor.Result{Name: "email test", Level: doctor.LevelFail, Detail: err.Error()}
			report.Failed = true
		}
		report.Results = append(report.Results, result)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, r := range report.Results {
			fmt.Fprintf(w, "[%s]\t%s\t%s\n", r.Level, r.Name, r.Detail)
		}
		w.Flush()
	}

	if report.Failed {
		return &exitError{code: 1, err: errors.New("doctor found problems")}
	}
	return nil
}

func cmdFreshness(ctx context.Context, args []string) error {
	fs := newFlagSet("freshness")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	maxAge := fs.Duration("max-age", 0, "override freshness.max_age")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	noNotify := fs.Bool("no-notify", false, "do not send alerts or pings")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *maxAge <= 0 {
		*maxAge = cfg.FreshnessMaxAge
	}

	st, err := state.Store{Path: cfg.StateFile}.Load()
	if err != nil {
		return err
	}
	result := freshness.Check(ctx, st, freshness.Options{
		MaxAge:             *maxAge,
		RespectBackupTimer: cfg.Freshness.RespectBackupTimer,
		Unit:               systemd.BackupTimerName(cfg.Job),
	})

	status := "success"
	if result.Kind != freshness.KindOK {
		status = "failed"
	}
	if !*noNotify {
		notifier := notify.New(notify.Options{
			Webhooks:         cfg.Notify.Webhooks,
			FreshnessPingURL: cfg.Notify.FreshnessPingURL,
			DedupFile:        cfg.AlertsFile,
			DedupBackup:      cfg.DedupBackup,
			DedupFreshness:   cfg.DedupFreshness,
			Email:            notify.EmailOptionsFromConfig(cfg),
			TelegramToken:    cfg.Notify.TelegramToken,
			TelegramChatID:   cfg.Notify.TelegramChatID,
			TelegramChatIDs:  cfg.Notify.TelegramChatIDs,
			NotifyOnSuccess:  cfg.Notify.NotifyOnSuccess,
			Timeout:          cfg.NotifyTimeout,
			Logger:           newLogger(*verbose),
		})
		ev := notify.Event{
			Event:  "freshness",
			Status: status,
			Host:   cfg.Host,
			Time:   time.Now(),
		}
		if status == "failed" {
			ev.Error = result.Message
		}
		if err := notifier.Send(ctx, ev); err != nil {
			fmt.Fprintf(os.Stderr, "jealous-vault: cannot send freshness alert: %v\n", err)
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else {
		fmt.Printf("freshness: %s\n", result.Kind)
		fmt.Println(result.Message)
	}

	if result.Kind != freshness.KindOK {
		return &exitError{code: 1, err: fmt.Errorf("freshness check: %s", result.Kind)}
	}
	return nil
}

type statusOutput struct {
	Stale       bool       `json:"stale"`
	Running     bool       `json:"running"`
	CurrentRun  *lock.Info `json:"current_run,omitempty"`
	AgeSeconds  float64    `json:"age_seconds"`
	MaxAge      string     `json:"max_age"`
	LastRun     *state.Run `json:"last_run,omitempty"`
	LastSuccess *state.Run `json:"last_success,omitempty"`
	LastCheck   *state.Run `json:"last_check,omitempty"`
	LastVerify  *state.Run `json:"last_verify,omitempty"`
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	maxAge := fs.Duration("max-age", 25*time.Hour, "fail when the last successful backup is older than this")
	asJSON := fs.Bool("json", false, "print status as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	store := state.Store{Path: cfg.StateFile}
	st, err := store.Load()
	if err != nil {
		return err
	}
	runInfo, running := lock.Inspect(cfg.LockFile)

	now := time.Now()
	var age time.Duration
	if st.LastSuccess != nil {
		age = now.Sub(st.LastSuccess.FinishedAt)
	}
	stale := st.LastSuccess == nil || (*maxAge > 0 && age > *maxAge)
	failed := st.LastRun != nil && st.LastRun.Status == state.StatusFailed

	out := statusOutput{
		Stale:       stale,
		Running:     running,
		AgeSeconds:  age.Seconds(),
		MaxAge:      maxAge.String(),
		LastRun:     st.LastRun,
		LastSuccess: st.LastSuccess,
		LastCheck:   st.LastCheck,
		LastVerify:  st.LastVerify,
	}
	if running && runInfo.PID != 0 {
		out.CurrentRun = &runInfo
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Printf("host:        %s\n", cfg.Host)
		fmt.Printf("job:         %s\n", cfg.Job)
		fmt.Printf("repository:  %s\n", cfg.RedactedRepository())
		switch {
		case running && runInfo.PID != 0:
			fmt.Printf("running:     %s since %s (pid %d)\n", runInfo.Command, runInfo.StartedAt.Format(time.RFC3339), runInfo.PID)
		case running:
			fmt.Println("running:     another process")
		default:
			fmt.Println("running:     no")
		}
		fmt.Println()
		printStatusLine("last run", st.LastRun, now)
		printStatusLine("last success", st.LastSuccess, now)
		printStatusLine("last check", st.LastCheck, now)
		printStatusLine("last verify", st.LastVerify, now)
		if stale {
			fmt.Printf("\nbackup is stale, max age %s\n", maxAge.String())
		}
	}

	if failed {
		return &exitError{code: 1, err: errors.New("last run failed")}
	}
	if stale {
		return &exitError{code: 1, err: fmt.Errorf("no successful backup within %s", maxAge.String())}
	}
	return nil
}

func cmdHistory(args []string) error {
	fs := newFlagSet("history")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	limit := fs.Int("limit", 20, "number of runs to show, 0 for all")
	asJSON := fs.Bool("json", false, "print the history as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	runs, err := (state.Store{Path: cfg.StateFile, HistoryPath: cfg.HistoryFile}).History(*limit)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	}
	if len(runs) == 0 {
		fmt.Println("no runs recorded")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TIME	KIND	STATUS	DURATION	SNAPSHOT	ERROR")
	for _, run := range runs {
		snapshot := run.SnapshotID
		if len(snapshot) > 12 {
			snapshot = snapshot[:12]
		}
		fmt.Fprintf(w, "%s	%s	%s	%s	%s	%s\n",
			run.FinishedAt.Local().Format("2006-01-02 15:04:05"),
			run.Kind,
			run.Status,
			time.Duration(run.DurationSeconds*float64(time.Second)).Round(time.Second),
			snapshot,
			truncate(run.Error, 60),
		)
	}
	return w.Flush()
}

func cmdRecoveryKit(args []string) error {
	fs := newFlagSet("recovery-kit")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	output := stringFlag(fs, "output", "", "file that will hold the recovery kit")
	force := fs.Bool("force", false, "overwrite an existing recovery kit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *output == "" {
		return errors.New("--output is required")
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	if err := recovery.Write(cfg, *output, *force, version); err != nil {
		return err
	}
	fmt.Printf("recovery kit: %s\n", *output)
	fmt.Println("It contains repository credentials. Store it offline and restrict access.")
	return nil
}

func printStatusLine(label string, run *state.Run, now time.Time) {
	if run == nil {
		fmt.Printf("%-13s never\n", label+":")
		return
	}
	age := now.Sub(run.FinishedAt).Round(time.Second)
	fmt.Printf("%-13s %s, %s ago\n", label+":", run.Status, age)
	if run.SnapshotID != "" {
		fmt.Printf("%-13s %s\n", "", "snapshot "+run.SnapshotID)
	}
	if run.Error != "" {
		fmt.Printf("%-13s %s\n", "", run.Error)
	}
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
