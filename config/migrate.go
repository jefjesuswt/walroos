package config

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies every migrations/NNN_name.sql newer than the stored schema version,
// each one in its own transaction.
func (db *Database) Migrate(ctx context.Context) error {
	if _, err := db.Write.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := db.Write.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/*.sql") // lexical order == numeric order with zero padding
	if err != nil {
		return err
	}

	// One connection for the whole run: foreign_keys is per connection, and a migration that
	// rebuilds a table others reference (SQLite's way to change a constraint) must run with them
	// off, or dropping the old table would cascade. foreign_key_check keeps it honest.
	conn, err := db.Write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)

	for _, f := range files {
		v, err := strconv.Atoi(strings.SplitN(path.Base(f), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %s: %w", f, err)
		}
		if v <= current {
			continue
		}

		body, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", f, err)
		}
		if rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: foreign key check: %w", f, err)
		} else {
			broken := rows.Next()
			rows.Close()
			if broken {
				tx.Rollback()
				return fmt.Errorf("migration %s leaves rows pointing at missing ones", f)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, v); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}
