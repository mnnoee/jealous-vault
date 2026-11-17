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
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/doctor"
	"github.com/mnnoee/jealous-vault/internal/hooks"
	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/restore"
	"github.com/mnnoee/jealous-vault/internal/runner"
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
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "init":
		return cmdInit(ctx, args[1:])
	case "repo":
		return cmdRepo(ctx, args[1:])
	case "install":
		return cmdInstall(ctx, args[1:])
	case "uninstall":
		return cmdUninstall(ctx, args[1:])
	case "backup":
		return cmdBackup(ctx, args[1:])
	case "snapshots":
		return cmdSnapshots(ctx, args[1:])
	case "mount":
		return cmdMount(ctx, args[1:])
	case "restore":
		return cmdRestore(ctx, args[1:])
	case "test-restore":
		return cmdTestRestore(ctx, args[1:])
	case "check":
		return cmdCheck(ctx, args[1:])
	case "doctor":
		return cmdDoctor(ctx, args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "version":
		fmt.Printf("jealous-vault %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q, run \"jealous-vault help\"", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `JealousVault, encrypted backups and predictable restores.

Usage:
  jealous-vault <command> [flags]

Commands:
  init          write a config, generate the password and init the repository
  repo init     initialize the repository for an existing config
  install       install the binary, systemd services and timers
  uninstall     stop the timers and remove the unit files
  backup        run a backup now
  snapshots     list snapshots in the repository
  mount         mount a snapshot read-only through FUSE
  restore       restore a snapshot or the latest one
  test-restore  restore into a separate directory
  check         verify repository integrity
  doctor        check restic, config, repository, paths and timers
  status        show the last run and whether the backup is fresh
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

func resticClient(cfg *config.Config, log *slog.Logger) *restic.Client {
	return restic.New(restic.Options{
		Binary:       cfg.ResticBinary,
		Repository:   cfg.Repository.URL,
		PasswordFile: cfg.Repository.PasswordFile,
		Env:          cfg.Repository.Env,
		Logger:       log,
		Output:       os.Stdout,
	})
}

func cmdInit(ctx context.Context, args []string) error {
	fs := newFlagSet("init")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file to write")
	host := stringFlag(fs, "host", "", "host name recorded in snapshots, defaults to the system hostname")
	repoURL := stringFlag(fs, "repo", "", "restic repository URL")
	paths := stringFlag(fs, "paths", "", "comma separated absolute paths to back up")
	excludes := stringFlag(fs, "excludes", "", "comma separated exclude patterns")
	profile := stringFlag(fs, "profile", "host", "backup profile: host, vm-dumps or custom")
	passwordFile := stringFlag(fs, "password-file", "", "file that will hold the repository password")
	stateFile := stringFlag(fs, "state-file", config.DefaultStateFile, "file that will hold the last run state")
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
		Host:          *host,
		RepositoryURL: *repoURL,
		PasswordFile:  *passwordFile,
		StateFile:     *stateFile,
		Profile:       *profile,
		Paths:         splitComma(*paths),
		Excludes:      splitComma(*excludes),
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
		return errors.New("usage: jealous-vault repo init [--config path]")
	}
	switch args[0] {
	case "init":
		fs := newFlagSet("repo init")
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
		return initRepository(ctx, cfg, newLogger(*verbose))
	default:
		return fmt.Errorf("unknown repo command %q", args[0])
	}
}

func initRepository(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
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
	if opts.StateFile == "" {
		opts.StateFile = config.DefaultStateFile
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
	return opts.PasswordFile, nil
}

func cmdInstall(ctx context.Context, args []string) error {
	fs := newFlagSet("install")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	prefix := stringFlag(fs, "prefix", "/usr/local/bin", "directory for the binary")
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
	log := newLogger(*verbose)
	if _, lookErr := exec.LookPath(cfg.ResticBinary); lookErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %s not found in PATH, install restic before the first backup\n", cfg.ResticBinary)
	}

	installer := &systemd.Installer{
		BinaryPath: filepath.Join(*prefix, "jealous-vault"),
		ConfigPath: *configPath,
		SkipStart:  *noStart,
		Out:        os.Stdout,
		Log:        log,
	}
	if err := installer.Install(ctx, cfg); err != nil {
		return err
	}

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	installer := &systemd.Installer{
		BinaryPath: filepath.Join(*prefix, "jealous-vault"),
		ConfigPath: *configPath,
		Out:        os.Stdout,
	}
	return installer.Uninstall(ctx)
}

func cmdBackup(ctx context.Context, args []string) error {
	fs := newFlagSet("backup")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print the run result as JSON")
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
	if *noPrune {
		cfg.Retention.Prune = false
	}

	log := newLogger(*verbose)
	r := runner.New(cfg, log, os.Stderr)
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
	client := resticClient(cfg, newLogger(*verbose))
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
	return client.Mount(ctx, target, os.Stdout, os.Stderr)
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
	yes := fs.Bool("yes", false, "confirm a restore into /")
	var includes stringList
	fs.Var(&includes, "include", "path inside the snapshot to restore, repeatable")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	plan, err := restore.New(*snapshot, *target, includes, *overwrite)
	if err != nil {
		return err
	}
	plan.Verify = *verify

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	plan.Host = cfg.Host
	fmt.Print(plan.Describe())
	if plan.Dangerous() && !*yes {
		return fmt.Errorf("%w, pass --yes", restore.ErrDangerous)
	}
	fmt.Println("restoring")

	log := newLogger(*verbose)
	if err := executeRestore(ctx, cfg, plan, log, "restore"); err != nil {
		return err
	}
	fmt.Println("restore finished")
	return nil
}

// executeRestore runs restore hooks around the plan, the same way a backup
// runs its own hooks.
func executeRestore(ctx context.Context, cfg *config.Config, plan *restore.Plan, log *slog.Logger, event string) error {
	env := map[string]string{
		"JM_EVENT":    event,
		"JM_HOST":     cfg.Host,
		"JM_SNAPSHOT": plan.Snapshot,
		"JM_TARGET":   plan.Target,
		"JM_STATUS":   "running",
		"JM_PHASE":    "pre",
	}
	if err := hooks.Run(ctx, log, event+"-pre", cfg.Restore.PreHooks, env); err != nil {
		env["JM_PHASE"] = "post"
		env["JM_STATUS"] = "failed"
		_ = hooks.Run(ctx, log, event+"-post", cfg.Restore.PostHooks, env)
		return err
	}

	restoreErr := plan.Execute(ctx, resticClient(cfg, log))
	status := "success"
	if restoreErr != nil {
		status = "failed"
	}
	env["JM_PHASE"] = "post"
	env["JM_STATUS"] = status
	if hookErr := hooks.Run(ctx, log, event+"-post", cfg.Restore.PostHooks, env); hookErr != nil {
		return errors.Join(restoreErr, hookErr)
	}
	return restoreErr
}

func cmdTestRestore(ctx context.Context, args []string) error {
	fs := newFlagSet("test-restore")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	snapshot := stringFlag(fs, "snapshot", "latest", "snapshot ID or latest")
	targetDir := stringFlag(fs, "target", "", "directory for the test restore")
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
	target := *targetDir
	if target == "" {
		target = restore.TestTarget("", time.Now())
	}
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}

	plan, err := restore.New(*snapshot, target, nil, restore.OverwriteAlways)
	if err != nil {
		return err
	}
	plan.Host = cfg.Host
	fmt.Printf("restoring %s into %s\n", plan.Snapshot, target)
	log := newLogger(*verbose)
	if err := executeRestore(ctx, cfg, plan, log, "test-restore"); err != nil {
		return err
	}
	fmt.Printf("test restore finished: %s\n", target)
	fmt.Println("inspect the files, then remove the directory")
	return nil
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

	report := doctor.Run(ctx, cfg, doctor.Options{Logger: newLogger(*verbose)})
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

type statusOutput struct {
	Stale       bool       `json:"stale"`
	AgeSeconds  float64    `json:"age_seconds"`
	MaxAge      string     `json:"max_age"`
	LastRun     *state.Run `json:"last_run,omitempty"`
	LastSuccess *state.Run `json:"last_success,omitempty"`
	LastCheck   *state.Run `json:"last_check,omitempty"`
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

	now := time.Now()
	var age time.Duration
	if st.LastSuccess != nil {
		age = now.Sub(st.LastSuccess.FinishedAt)
	}
	stale := st.LastSuccess == nil || (*maxAge > 0 && age > *maxAge)
	failed := st.LastRun != nil && st.LastRun.Status == state.StatusFailed

	out := statusOutput{
		Stale:       stale,
		AgeSeconds:  age.Seconds(),
		MaxAge:      maxAge.String(),
		LastRun:     st.LastRun,
		LastSuccess: st.LastSuccess,
		LastCheck:   st.LastCheck,
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Printf("host:        %s\n", cfg.Host)
		fmt.Printf("repository:  %s\n", cfg.RedactedRepository())
		fmt.Println()
		printStatusLine("last run", st.LastRun, now)
		printStatusLine("last success", st.LastSuccess, now)
		printStatusLine("last check", st.LastCheck, now)
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
