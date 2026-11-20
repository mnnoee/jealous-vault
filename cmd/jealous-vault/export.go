// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/restic"
)

// resticExport is the machine-readable form of `export-restic-env`.
type resticExport struct {
	Job           string   `json:"job"`
	Profile       string   `json:"profile"`
	Repository    string   `json:"repository"`
	PasswordFile  string   `json:"password_file,omitempty"`
	CacheDir      string   `json:"cache_dir,omitempty"`
	EnvFile       string   `json:"env_file,omitempty"`
	EnvKeys       []string `json:"env_keys,omitempty"`
	ExtraArgs     []string `json:"extra_args,omitempty"`
	BackupCommand []string `json:"backup_command"`
	ForgetCommand []string `json:"forget_command,omitempty"`
	Shell         string   `json:"shell"`
}

func cmdExportResticEnv(args []string) error {
	fs := newFlagSet("export-restic-env")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	asJSON := fs.Bool("json", false, "print the export as JSON")
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

	export := buildResticExport(cfg)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(export)
	}
	fmt.Print(export.Shell)
	return nil
}

func buildResticExport(cfg *config.Config) resticExport {
	client := restic.New(restic.Options{
		Binary:       cfg.ResticBinary,
		Repository:   cfg.Repository.URL,
		PasswordFile: cfg.Repository.PasswordFile,
		Env:          cfg.Repository.Env,
		CacheDir:     cfg.ResticCacheDir(),
		Extra:        cfg.ResticExtraOptions(),
	})

	export := resticExport{
		Job:          cfg.Job,
		Profile:      cfg.Backup.Profile,
		Repository:   cfg.Repository.URL,
		PasswordFile: cfg.Repository.PasswordFile,
		EnvFile:      cfg.Repository.EnvFile,
		ExtraArgs:    cfg.ResticExtraOptions(),
	}
	entries := client.EnvironmentEntries()
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "RESTIC_CACHE_DIR":
			export.CacheDir = value
		case "RESTIC_REPOSITORY":
			export.Repository = value
		case "RESTIC_PASSWORD_FILE":
			export.PasswordFile = value
		}
		if !strings.HasPrefix(key, "RESTIC_") {
			export.EnvKeys = append(export.EnvKeys, key)
		}
	}
	sort.Strings(export.EnvKeys)

	paths := append([]string(nil), cfg.Backup.Paths...)
	dumps := dumpsEnabled(cfg.Backup.Dumps)
	if dumps {
		paths = append(paths, cfg.Backup.Dumps.Dir)
	}
	opts := restic.BackupOptions{
		Host:          cfg.Host,
		Paths:         paths,
		Excludes:      cfg.Backup.Excludes,
		Tags:          cfg.Backup.Tags,
		OneFileSystem: cfg.Backup.OneFileSystem,
		LimitUpload:   cfg.Backup.LimitUpload,
		Compression:   cfg.Backup.Compression,
		PackSize:      cfg.Backup.PackSize,
	}

	base := append([]string{cfg.ResticBinary}, cfg.ResticExtraOptions()...)
	export.BackupCommand = append(append([]string(nil), base...), restic.BackupArgs(opts)...)
	if cfg.HasRetention() {
		forget := append(append([]string(nil), base...), "forget")
		forget = append(forget, cfg.RetentionArgs()...)
		if cfg.Retention.Prune {
			forget = append(forget, "--prune")
		}
		export.ForgetCommand = forget
	}

	export.Shell = resticExportShell(cfg, export, dumps)
	return export
}

func dumpsEnabled(d config.DumpOptions) bool {
	return (d.Postgres != nil && d.Postgres.Enabled) || (d.MySQL != nil && d.MySQL.Enabled)
}

func resticExportShell(cfg *config.Config, export resticExport, dumps bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# JealousVault export: job %q, profile %q\n", export.Job, export.Profile)
	fmt.Fprintf(&b, "# Repository: %s\n", cfg.RedactedRepository())
	b.WriteString("# This output may contain repository credentials. Do not paste it into tickets.\n")

	fmt.Fprintf(&b, "export RESTIC_REPOSITORY=%s\n", shellQuote(export.Repository))
	if export.PasswordFile != "" {
		fmt.Fprintf(&b, "export RESTIC_PASSWORD_FILE=%s\n", shellQuote(export.PasswordFile))
	}
	if export.CacheDir != "" {
		fmt.Fprintf(&b, "export RESTIC_CACHE_DIR=%s\n", shellQuote(export.CacheDir))
	}

	if export.EnvFile != "" {
		b.WriteString("# Load repository credentials and other backend variables:\n")
		b.WriteString("set -a\n")
		fmt.Fprintf(&b, ". %s\n", shellQuote(export.EnvFile))
		b.WriteString("set +a\n")
	} else if len(export.EnvKeys) > 0 {
		fmt.Fprintf(&b, "# repository.env also sets: %s\n", strings.Join(export.EnvKeys, ", "))
		b.WriteString("# Export those variables in this shell before running restic.\n")
	}
	if export.PasswordFile == "" {
		if cfg.Repository.Env["RESTIC_PASSWORD_COMMAND"] != "" {
			b.WriteString("# RESTIC_PASSWORD_COMMAND provides the repository password.\n")
		} else {
			b.WriteString("# RESTIC_PASSWORD is not stored in a file; export it in this shell.\n")
			b.WriteString(`: "${RESTIC_PASSWORD:?set RESTIC_PASSWORD first}"` + "\n")
		}
	}

	b.WriteString("\n# Backup:\n")
	b.WriteString(shellJoin(export.BackupCommand) + "\n")
	if dumps {
		fmt.Fprintf(&b, "# The dump directory %s is included; run the database dumps first.\n", cfg.Backup.Dumps.Dir)
	}
	if len(cfg.Backup.PreHooks) > 0 || len(cfg.Backup.PostHooks) > 0 {
		b.WriteString("# Pre/post hooks are not included; use \"jealous-vault backup\" for the full workflow.\n")
	}

	if len(export.ForgetCommand) > 0 {
		b.WriteString("\n# Retention:\n")
		b.WriteString(shellJoin(export.ForgetCommand) + "\n")
	}

	base := append([]string{cfg.ResticBinary}, cfg.ResticExtraOptions()...)
	b.WriteString("\n# Restore:\n")
	b.WriteString(shellJoin(append(append([]string(nil), base...), "snapshots")) + "\n")
	b.WriteString(shellJoin(append(append([]string(nil), base...), "restore", "latest", "--target", "/")) + "\n")
	return b.String()
}

var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if safeShellWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
