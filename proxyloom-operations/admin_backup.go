// Backup tools run only in the one-shot operations executable.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/Runarry/ProxyLoom/internal/backup"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/storage"
)

func backupKeyFiles(args []string, output io.Writer) error {
	if len(args) != 5 || args[1] != "--identity-output" || args[3] != "--recipient-output" || !filepath.IsAbs(args[2]) || !filepath.IsAbs(args[4]) || args[2] == args[4] {
		return errors.New("admin_invalid_backup_key_arguments")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return backup.ErrKeys
	}
	created := []string{}
	complete := false
	defer func() {
		if !complete {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for _, item := range []struct{ path, value string }{{args[2], identity.String()}, {args[4], identity.Recipient().String()}} {
		file, err := os.OpenFile(item.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return backup.ErrWrite
		}
		created = append(created, item.path)
		_, writeErr := io.WriteString(file, item.value+"\n")
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return backup.ErrWrite
		}
	}
	complete = true
	return json.NewEncoder(output).Encode(map[string]bool{"created": true})
}

func adminBackup(ctx context.Context, args []string, lookup config.Lookup, output io.Writer) error {
	if args[0] == "create-backup-key" {
		return backupKeyFiles(args, output)
	}
	creating := args[0] == "backup"
	if creating && (len(args) != 3 || (args[1] != "--output" && args[1] != "--directory")) || !creating && (len(args) != 5 || args[1] != "--input" || args[3] != "--identity-file") {
		return errors.New("admin_invalid_backup_arguments")
	}
	if !filepath.IsAbs(args[2]) {
		return errors.New("admin_backup_absolute_path_required")
	}
	keys, err := (config.API{MasterKeyID: lookup("PROXYLOOM_MASTER_KEY_ID"), MasterKeyFile: lookup("PROXYLOOM_MASTER_KEY_FILE"), OldMasterKeysFile: lookup("PROXYLOOM_OLD_MASTER_KEYS_FILE"), TokenPepperFile: lookup("PROXYLOOM_TOKEN_PEPPER_FILE"), ContentHMACKeyFile: lookup("PROXYLOOM_CONTENT_HMAC_KEY_FILE")}).ReadKeys()
	if err != nil {
		return backup.ErrKeys
	}
	defer keys.Clear()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	toolsDir := lookup("PROXYLOOM_PG_TOOLS_DIR")
	if toolsDir == "" {
		toolsDir = "/usr/lib/postgresql/17/bin"
	}
	if creating {
		recipient, err := readAdminFile(lookup("PROXYLOOM_BACKUP_RECIPIENT_FILE"), 2048)
		if err != nil {
			return backup.ErrKeys
		}
		defer clear(recipient)
		cfg, err := config.LoadMigration(lookup)
		if err != nil {
			return backup.ErrDatabase
		}
		dsn, err := cfg.ReadDSN()
		if err != nil {
			return backup.ErrDatabase
		}
		pool, err := storage.Open(ctx, dsn)
		if err != nil {
			return backup.ErrDatabase
		}
		defer pool.Close()
		unlock, err := storage.LockBackup(ctx, pool)
		if err != nil {
			return backup.ErrDatabase
		}
		defer unlock()
		status, err := storage.MigrationStatus(ctx, pool)
		if err != nil || status.Applied < 1 {
			return errors.New("backup_schema_incompatible")
		}
		required, err := storage.RequiredMasterKeyIDs(ctx, pool, status.Applied)
		if err != nil {
			return backup.ErrKeys
		}
		for _, id := range required {
			if len(keys.MasterKeys[id]) != 32 {
				return backup.ErrKeys
			}
		}
		destination := args[2]
		if args[1] == "--directory" {
			destination = filepath.Join(destination, "proxyloom-"+time.Now().UTC().Format("20060102T150405Z")+"-"+string(jobs.NewID())+".age")
		}
		result, err := backup.Snapshot(ctx, toolsDir, dsn, destination, string(recipient), keys, int64(status.Applied))
		if err != nil {
			return err
		}
		if status.Applied >= 20 {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return errors.New("backup_status_record_failed")
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `INSERT INTO public.maintenance_status(scope_id,last_backup_at) SELECT id,$1 FROM public.scopes ON CONFLICT(scope_id) DO UPDATE SET last_backup_at=EXCLUDED.last_backup_at`, result.CreatedAt); err != nil {
				return errors.New("backup_status_record_failed")
			}
			if _, err = tx.Exec(ctx, `INSERT INTO public.system_audit_events(scope_id,action,request_id) SELECT id,'backup','scheduled-backup' FROM public.scopes`); err != nil {
				return errors.New("backup_status_record_failed")
			}
			if err = tx.Commit(ctx); err != nil {
				return errors.New("backup_status_record_failed")
			}
		}
		return json.NewEncoder(output).Encode(result)
	}
	identity, err := readAdminFile(args[4], 2048)
	if err != nil {
		return backup.ErrKeys
	}
	defer clear(identity)
	input, err := os.Open(args[2])
	if err != nil {
		return backup.ErrArchive
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return backup.ErrArchive
	}
	// Copy ciphertext into an owned private file before the verification and
	// restore passes. No plaintext database dump is written to the filesystem.
	workDir := lookup("PROXYLOOM_RESTORE_WORK_DIR")
	if workDir != "" && !filepath.IsAbs(workDir) {
		return errors.New("admin_backup_absolute_path_required")
	}
	copyFile, err := os.CreateTemp(workDir, "proxyloom-restore-*.age")
	if err != nil {
		return backup.ErrWrite
	}
	defer func() { copyFile.Close(); os.Remove(copyFile.Name()) }()
	if _, err = io.Copy(copyFile, input); err != nil {
		return backup.ErrArchive
	}
	if _, err = copyFile.Seek(0, io.SeekStart); err != nil {
		return backup.ErrArchive
	}
	manifest, err := backup.Verify(copyFile, string(identity), keys)
	if err != nil {
		return err
	}
	if args[0] == "verify-backup" {
		return json.NewEncoder(output).Encode(map[string]any{"verified": true, "backup_id": manifest.BackupID, "created_at": manifest.CreatedAt, "database_schema": manifest.DatabaseSchema})
	}
	if args[0] != "restore-backup" {
		return errors.New("admin_invalid_backup_arguments")
	}
	cfg, err := config.LoadMigration(lookup)
	if err != nil {
		return backup.ErrDatabase
	}
	dsn, err := cfg.ReadDSN()
	if err != nil {
		return backup.ErrDatabase
	}
	if _, err = backup.Restore(ctx, toolsDir, dsn, copyFile, string(identity), keys); err != nil {
		return err
	}
	pool, err := storage.Open(ctx, dsn)
	if err != nil {
		return backup.ErrDatabase
	}
	defer pool.Close()
	epoch, err := storage.ControlAuthorizationEpoch(ctx, pool)
	if err != nil || epoch == "" {
		return errors.New("restore_authorization_reset_missing")
	}
	return json.NewEncoder(output).Encode(map[string]any{"restored": true, "backup_id": manifest.BackupID, "source_schema": manifest.DatabaseSchema, "runner_authorization_epoch": epoch})
}
