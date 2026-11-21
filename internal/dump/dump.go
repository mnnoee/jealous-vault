// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package dump creates consistent PostgreSQL and MySQL dumps before a backup.
package dump

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/executil"
	"github.com/mnnoee/jealous-vault/internal/safepath"
)

// Manager collects database dumps in timestamped directories.
type Manager struct {
	opts          config.DumpOptions
	log           *slog.Logger
	now           func() time.Time
	showSensitive bool
}

// New returns a Manager. A nil logger discards output.
func New(opts config.DumpOptions, log *slog.Logger) *Manager {
	return NewWithSensitive(opts, log, false)
}

// NewWithSensitive is New with an explicit sensitive-output preference.
func NewWithSensitive(opts config.DumpOptions, log *slog.Logger, showSensitive bool) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &Manager{opts: opts, log: log, now: time.Now, showSensitive: showSensitive}
}

// Enabled reports whether at least one database backend is configured.
func (m *Manager) Enabled() bool {
	return m != nil && ((m.opts.Postgres != nil && m.opts.Postgres.Enabled) ||
		(m.opts.MySQL != nil && m.opts.MySQL.Enabled))
}

// Run creates one dump directory and fills it. It returns the directory even
// when a dump fails so the operator can inspect the partial result.
func (m *Manager) Run(ctx context.Context) (string, error) {
	if !m.Enabled() {
		return "", nil
	}
	if err := os.MkdirAll(m.opts.Dir, 0o750); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(m.opts.Dir, "dump-"+m.now().UTC().Format("20060102-150405")+"-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return dir, err
	}

	if p := m.opts.Postgres; p != nil && p.Enabled {
		if err := m.dumpPostgres(ctx, dir, *p); err != nil {
			return dir, err
		}
	}
	if my := m.opts.MySQL; my != nil && my.Enabled {
		if err := m.dumpMySQL(ctx, dir, *my); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func (m *Manager) dumpPostgres(ctx context.Context, dir string, p config.PostgresDump) error {
	env := executil.Environment()
	if p.PasswordFile != "" {
		password, err := safepath.ReadFile(p.PasswordFile)
		if err != nil {
			return fmt.Errorf("postgres password: %w", err)
		}
		env = append(env, "PGPASSWORD="+strings.TrimSpace(string(password)))
	}
	for _, database := range p.Databases {
		file := filepath.Join(dir, "postgres-"+safeName(database)+".dump")
		args := []string{"--format=custom", "--file=" + file, "--dbname=" + database}
		if p.Host != "" {
			args = append(args, "--host="+p.Host)
		}
		if p.Port > 0 {
			args = append(args, fmt.Sprintf("--port=%d", p.Port))
		}
		if p.User != "" {
			args = append(args, "--username="+p.User)
		}
		m.log.Info("dumping postgres", "database", database, "file", file)
		if err := runCommand(ctx, p.Binary, args, env, m.log, m.showSensitive); err != nil {
			return fmt.Errorf("pg_dump %s: %w", database, err)
		}
		if err := os.Chmod(file, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) dumpMySQL(ctx context.Context, dir string, my config.MySQLDump) error {
	defaults := ""
	env := executil.Environment()
	if my.PasswordFile != "" {
		password, err := safepath.ReadFile(my.PasswordFile)
		if err != nil {
			return fmt.Errorf("mysql password: %w", err)
		}
		tmp, err := os.MkdirTemp("", "jv-mysql-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		defaults = filepath.Join(tmp, ".mysql.cnf")
		escaped := strings.NewReplacer(
			`\`, `\\`,
			`"`, `\"`,
			"\n", `\n`,
			"\r", `\r`,
		).Replace(strings.TrimSpace(string(password)))
		body := "[client]\npassword=\"" + escaped + "\"\n"
		if err := os.WriteFile(defaults, []byte(body), 0o600); err != nil {
			return err
		}
		// The client reads ~/.my.cnf after --defaults-extra-file, so point
		// HOME at the private temp directory. There is no user option file
		// there, and global option files still apply.
		env = envWithHome(env, tmp)
	}
	for _, database := range my.Databases {
		file := filepath.Join(dir, "mysql-"+safeName(database)+".sql")
		var args []string
		if defaults != "" {
			args = append(args, "--defaults-extra-file="+defaults)
		}
		args = append(args,
			"--single-transaction",
			"--routines",
			"--events",
			"--triggers",
			"--result-file="+file,
		)
		if my.Host != "" {
			args = append(args, "--host="+my.Host)
		}
		if my.Port > 0 {
			args = append(args, fmt.Sprintf("--port=%d", my.Port))
		}
		if my.User != "" {
			args = append(args, "--user="+my.User)
		}
		args = append(args, database)
		m.log.Info("dumping mysql", "database", database, "file", file)
		if err := runCommand(ctx, my.Binary, args, env, m.log, m.showSensitive); err != nil {
			return fmt.Errorf("mysqldump %s: %w", database, err)
		}
		if err := os.Chmod(file, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Prune keeps the newest opts.Keep dump directories. It is meant to run after
// a successful backup, so keep=0 removes the dump that was just archived.
func (m *Manager) Prune() error {
	if !m.Enabled() || m.opts.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.opts.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "dump-") {
			dirs = append(dirs, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	keep := m.opts.Keep
	if keep < 0 {
		keep = 0
	}
	for i, name := range dirs {
		if i < keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.opts.Dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func runCommand(ctx context.Context, binary string, args, env []string, log *slog.Logger, showSensitive bool) error {
	cmd := executil.Command(ctx, binary, args...)
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if log != nil {
		if text := strings.TrimSpace(output.String()); text != "" {
			if showSensitive {
				if len(text) > 2048 {
					text = text[:2048] + "..."
				}
				log.Debug("dump output", "command", binary, "output", text, "error", err)
			} else {
				log.Info("dump output hidden; set logging.show_sensitive = true to print it",
					"command", filepath.Base(binary), "bytes", len(text))
			}
		} else {
			log.Debug("dump command", "command", binary, "args", args, "error", err)
		}
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s failed: %w", filepath.Base(binary), err)
}

// envWithHome replaces HOME in env so the MySQL client cannot read the
// caller's ~/.my.cnf.
func envWithHome(env []string, home string) []string {
	out := make([]string, 0, len(env)+1)
	for _, value := range env {
		if strings.HasPrefix(value, "HOME=") {
			continue
		}
		out = append(out, value)
	}
	return append(out, "HOME="+home)
}

func safeName(name string) string {
	return strings.NewReplacer("/", "_", `\`, "_", "..", "_").Replace(name)
}
