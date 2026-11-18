// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/restore"
)

// legacyFunc is one of the existing command implementations. They parse their
// own stdlib flag sets, so the Cobra layer rebuilds the arguments from the
// parsed flags instead of rewriting every command.
type legacyFunc func(context.Context, []string) error

// newRootCommand builds the command tree. Flags are defined once here, which
// makes shell completion and help accurate; the business logic stays in the
// cmd* functions.
func newRootCommand(ctx context.Context) *cobra.Command {
	root := &cobra.Command{
		Use:           "jealous-vault",
		Short:         "Encrypted backups and predictable restores",
		Long:          "JealousVault backs up Linux and Proxmox hosts to restic repositories and makes restoring them a single command.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.AddCommand(
		initCommand(ctx),
		repoCommand(ctx),
		installCommand(ctx),
		uninstallCommand(ctx),
		backupCommand(ctx),
		vzdumpCommand(ctx),
		restoreVMCommand(ctx),
		snapshotsCommand(ctx),
		mountCommand(ctx),
		restoreCommand(ctx),
		testRestoreCommand(ctx),
		checkCommand(ctx),
		doctorCommand(ctx),
		freshnessCommand(ctx),
		statusCommand(ctx),
		historyCommand(),
		tickCommand(ctx),
		recoveryKitCommand(ctx),
		versionCommand(ctx),
	)
	root.InitDefaultCompletionCmd()
	return root
}

func legacyCommand(ctx context.Context, use, short string, fn legacyFunc, flags func(*cobra.Command)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(c *cobra.Command, args []string) error {
			rebuilt, err := rebuildArgs(c, args)
			if err != nil {
				return err
			}
			return fn(ctx, rebuilt)
		},
	}
	if flags != nil {
		flags(cmd)
	}
	return cmd
}

// rebuildArgs turns the flags Cobra parsed back into the "--name=value" form
// the existing stdlib flag sets expect. Only flags the user actually changed
// are forwarded, so the command keeps its own defaults.
func rebuildArgs(c *cobra.Command, positional []string) ([]string, error) {
	var args []string
	var err error
	c.Flags().Visit(func(f *pflag.Flag) {
		if err != nil || !f.Changed {
			return
		}
		switch f.Value.Type() {
		case "string":
			var value string
			value, err = c.Flags().GetString(f.Name)
			if err == nil {
				args = append(args, "--"+f.Name+"="+value)
			}
		case "bool":
			var value bool
			value, err = c.Flags().GetBool(f.Name)
			if err == nil {
				args = append(args, fmt.Sprintf("--%s=%t", f.Name, value))
			}
		case "int":
			var value int
			value, err = c.Flags().GetInt(f.Name)
			if err == nil {
				args = append(args, "--"+f.Name+"="+fmt.Sprint(value))
			}
		case "duration":
			var value time.Duration
			value, err = c.Flags().GetDuration(f.Name)
			if err == nil {
				args = append(args, "--"+f.Name+"="+value.String())
			}
		case "stringArray":
			var values []string
			values, err = c.Flags().GetStringArray(f.Name)
			for _, value := range values {
				args = append(args, "--"+f.Name+"="+value)
			}
		case "stringSlice":
			var values []string
			values, err = c.Flags().GetStringSlice(f.Name)
			for _, value := range values {
				args = append(args, "--"+f.Name+"="+value)
			}
		default:
			err = fmt.Errorf("unsupported flag type %q for --%s", f.Value.Type(), f.Name)
		}
	})
	if err != nil {
		return nil, err
	}
	return append(args, positional...), nil
}

func configFlag(c *cobra.Command) {
	c.Flags().StringP("config", "c", config.DefaultPath, "configuration file")
}

func verboseFlag(c *cobra.Command) {
	c.Flags().Bool("verbose", false, "debug logging")
}

func initCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "init", "write a config, generate the password and init the repository", cmdInit, func(c *cobra.Command) {
		c.Flags().String("config", config.DefaultPath, "configuration file to write")
		c.Flags().String("host", "", "host name recorded in snapshots, defaults to the system hostname")
		c.Flags().String("job", "default", "job name, used in unit and state paths")
		c.Flags().String("repo", "", "restic repository URL")
		c.Flags().String("paths", "", "comma separated absolute paths to back up")
		c.Flags().String("excludes", "", "comma separated exclude patterns")
		c.Flags().String("profile", "host", "backup profile: host, vm-dumps or custom")
		c.Flags().String("password-file", "", "file that will hold the repository password")
		c.Flags().String("cache-dir", config.DefaultCacheDir, "directory for the restic index cache")
		c.Flags().String("state-file", "", "file that will hold the last run state, defaults per job")
		c.Flags().String("sftp-key", "", "private key for sftp repositories")
		c.Flags().String("sftp-known-hosts", "", "known_hosts file for sftp repositories")
		c.Flags().Bool("proxmox", false, "enable the Proxmox vzdump timer")
		c.Flags().Bool("force", false, "overwrite an existing configuration")
		c.Flags().Bool("no-repo-init", false, "only write the config and password")
		c.Flags().Bool("verbose", false, "debug logging")
	})
}

func repoCommand(ctx context.Context) *cobra.Command {
	repo := &cobra.Command{
		Use:   "repo",
		Short: "init, unlock, stats, diff, delete, copy, passwd, key",
	}

	initCmd := legacyCommand(ctx, "init", "initialize the repository of an existing config", cmdRepoInit, func(c *cobra.Command) {
		configFlag(c)
		verboseFlag(c)
	})
	unlockCmd := legacyCommand(ctx, "unlock", "remove stale restic locks", cmdRepoUnlock, func(c *cobra.Command) {
		configFlag(c)
		verboseFlag(c)
	})
	statsCmd := legacyCommand(ctx, "stats", "repository statistics", cmdRepoStats, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("mode", "restore-size", "restore-size, files-by-contents, raw-data or blobs-per-file")
		c.Flags().Bool("json", false, "print stats as JSON")
		verboseFlag(c)
	})
	diffCmd := legacyCommand(ctx, "diff", "differences between two snapshots", cmdRepoDiff, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("json", false, "print restic diff JSON")
		verboseFlag(c)
	})
	deleteCmd := legacyCommand(ctx, "delete", "delete a snapshot, requires --yes", cmdRepoDelete, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("yes", false, "confirm snapshot deletion")
		c.Flags().Bool("prune", false, "prune unreferenced data after deletion")
		verboseFlag(c)
	})
	copyCmd := legacyCommand(ctx, "copy", "copy snapshots to the second repository", cmdRepoCopy, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("all", false, "copy every snapshot instead of only missing ones")
		verboseFlag(c)
	})
	passwdCmd := legacyCommand(ctx, "passwd", "add a new repository key", cmdRepoPasswd, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("new-password-file", "", "file for the new repository password")
		verboseFlag(c)
	})

	keyCmd := &cobra.Command{Use: "key", Short: "list and remove repository keys"}
	keyList := legacyCommand(ctx, "list", "list repository keys", cmdRepoKey, func(c *cobra.Command) {
		configFlag(c)
		verboseFlag(c)
	})
	keyRemove := legacyCommand(ctx, "remove <id>", "remove a repository key, requires --yes", cmdRepoKey, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("yes", false, "confirm key removal")
		verboseFlag(c)
	})
	// cmdRepoKey dispatches on its own first argument.
	keyList.RunE = func(c *cobra.Command, args []string) error {
		rebuilt, err := rebuildArgs(c, args)
		if err != nil {
			return err
		}
		return cmdRepoKey(ctx, append([]string{"list"}, rebuilt...))
	}
	keyRemove.RunE = func(c *cobra.Command, args []string) error {
		rebuilt, err := rebuildArgs(c, args)
		if err != nil {
			return err
		}
		return cmdRepoKey(ctx, append([]string{"remove"}, rebuilt...))
	}
	keyCmd.AddCommand(keyList, keyRemove)

	repo.AddCommand(initCmd, unlockCmd, statsCmd, diffCmd, deleteCmd, copyCmd, passwdCmd, keyCmd)
	return repo
}

func installCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "install", "install the binary, services and timers", cmdInstall, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("prefix", "/usr/local/bin", "directory for the binary")
		c.Flags().String("init", "", "scheduler: systemd, openrc or cron, detected when empty")
		c.Flags().String("unit-dir", "", "override the systemd unit directory")
		c.Flags().Bool("no-start", false, "write units but do not start the timers")
		verboseFlag(c)
	})
}

func uninstallCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "uninstall", "stop the timers and remove unit files", cmdUninstall, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("prefix", "/usr/local/bin", "directory of the binary")
		c.Flags().String("job", "", "job name, defaults to the config value")
		c.Flags().String("init", "", "scheduler: systemd, openrc or cron, detected when empty")
	})
}

func backupCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "backup", "run a backup now, then retention and alerts", cmdBackup, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("json", false, "print the run result as JSON")
		c.Flags().Bool("dry-run", false, "show what would be backed up without writing")
		c.Flags().Bool("no-prune", false, "skip repository pruning after retention")
		verboseFlag(c)
	})
}

func vzdumpCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "vzdump", "dump Proxmox VMs and back the archives up", cmdVzdump, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("all", false, "dump every VM and CT")
		c.Flags().String("mode", "", "override proxmox.mode")
		c.Flags().String("compress", "", "override proxmox.compress")
		c.Flags().Int("keep", -1, "override proxmox.keep_last")
		verboseFlag(c)
	})
}

func restoreVMCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "restore-vm", "restore a VM or CT archive from a snapshot", cmdRestoreVM, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("storage", "", "override proxmox.storage")
		c.Flags().Bool("force", false, "overwrite an existing VM or CT")
		c.Flags().String("target", "", "directory for the temporary archive restore")
		c.Flags().Bool("keep", false, "keep the temporary archive directory")
		verboseFlag(c)
	})
}

func snapshotsCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "snapshots", "list snapshots in the repository", cmdSnapshots, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("json", false, "print snapshots as JSON")
		c.Flags().Bool("all", false, "list snapshots from every host")
		verboseFlag(c)
	})
}

func mountCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "mount", "mount a snapshot read-only through FUSE", cmdMount, func(c *cobra.Command) {
		configFlag(c)
		verboseFlag(c)
	})
}

func restoreCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "restore", "restore a snapshot or the latest one", cmdRestore, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("snapshot", "latest", "snapshot ID or latest")
		c.Flags().String("target", "/", "directory to restore into")
		c.Flags().String("overwrite", restore.OverwriteAlways, "always, never or if-changed")
		c.Flags().Bool("verify", false, "verify restored files against the repository")
		c.Flags().Bool("estimate", false, "print the restore size without writing anything")
		c.Flags().String("recipe", "", "named restore recipe from the config")
		c.Flags().Bool("yes", false, "confirm a restore into /")
		c.Flags().Bool("json", false, "print the estimate as JSON")
		c.Flags().StringArray("include", nil, "path inside the snapshot to restore, repeatable")
		verboseFlag(c)
	})
}

func testRestoreCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "test-restore", "restore into a separate directory and verify", cmdTestRestore, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("snapshot", "latest", "snapshot ID or latest")
		c.Flags().String("target", "", "directory for the test restore")
		c.Flags().String("level", "", "files or service, defaults to verify.level")
		c.Flags().String("max-size", "", "override verify.max_size")
		c.Flags().String("min-free", "", "override verify.min_free")
		c.Flags().String("post-hook", "", "override verify.post_hook")
		c.Flags().Duration("timeout", 0, "override verify.timeout")
		c.Flags().Bool("keep", false, "keep an automatic target directory on success")
		c.Flags().Bool("no-notify", false, "do not send alerts")
		c.Flags().Bool("yes", false, "confirm a restore into a system directory")
		c.Flags().Bool("json", false, "print the result as JSON")
		verboseFlag(c)
	})
}

func checkCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "check", "verify repository integrity", cmdCheck, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().String("read-data-subset", "", "override check.read_data_subset, for example 5%")
		c.Flags().Bool("json", false, "print the run result as JSON")
		verboseFlag(c)
	})
}

func doctorCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "doctor", "check restic, config, repository, paths and timers", cmdDoctor, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Bool("json", false, "print the report as JSON")
		c.Flags().Bool("send-test-email", false, "send one test email and report the result")
		verboseFlag(c)
	})
}

func freshnessCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "freshness", "check the backup age and the backup timer", cmdFreshness, func(c *cobra.Command) {
		configFlag(c)
		c.Flags().Duration("max-age", 0, "override freshness.max_age")
		c.Flags().Bool("json", false, "print the result as JSON")
		c.Flags().Bool("no-notify", false, "do not send alerts or pings")
		verboseFlag(c)
	})
}

func statusCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "status", "show the last run and whether the backup is fresh",
		func(_ context.Context, args []string) error { return cmdStatus(args) },
		func(c *cobra.Command) {
			configFlag(c)
			c.Flags().Duration("max-age", 25*time.Hour, "fail when the last successful backup is older than this")
			c.Flags().Bool("json", false, "print status as JSON")
		})
}

func historyCommand() *cobra.Command {
	return legacyCommand(context.Background(), "history", "show recent backup, check and verify runs",
		func(_ context.Context, args []string) error { return cmdHistory(args) },
		func(c *cobra.Command) {
			configFlag(c)
			c.Flags().Int("limit", 20, "number of runs to show, 0 for all")
			c.Flags().Bool("json", false, "print the history as JSON")
		})
}

func tickCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "tick", "run whatever is due and exit, for cron and simple init loops", cmdTick, func(c *cobra.Command) {
		configFlag(c)
		verboseFlag(c)
	})
}

func recoveryKitCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "recovery-kit", "write an offline recovery kit for this repository",
		func(_ context.Context, args []string) error { return cmdRecoveryKit(args) },
		func(c *cobra.Command) {
			configFlag(c)
			c.Flags().String("output", "", "file that will hold the recovery kit")
			c.Flags().Bool("force", false, "overwrite an existing recovery kit")
		})
}

func versionCommand(ctx context.Context) *cobra.Command {
	return legacyCommand(ctx, "version", "print version information",
		func(_ context.Context, _ []string) error {
			fmt.Printf("jealous-vault %s (commit %s, built %s)\n", version, commit, date)
			return nil
		}, nil)
}
