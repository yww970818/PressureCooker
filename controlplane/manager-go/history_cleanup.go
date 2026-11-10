package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type historyCleanupClaim struct {
	ID, ProjectID, Token string
	Data                 Obj
	Attempts             int
}

func (p *Platform) historyCleanupLoop(ctx context.Context) {
	for ctx.Err() == nil {
		err := p.cleanupHistory(ctx)
		p.recordLoop("history_cleanup", err)
		if !pause(ctx, 5*time.Second) {
			return
		}
	}
}
func (p *Platform) cleanupHistory(ctx context.Context) error {
	for i := 0; i < 20; i++ {
		var c historyCleanupClaim
		found := false
		err := p.transactionContext(ctx, func(tx *Tx) error {
			found = false
			c = historyCleanupClaim{}
			var raw string
			e := tx.QueryRow("SELECT id,project_id,data,attempts FROM artifact_cleanups WHERE next_attempt_at<=? AND lease_until<=? ORDER BY created_at,id LIMIT 1 FOR UPDATE", epoch(), epoch()).Scan(&c.ID, &c.ProjectID, &raw, &c.Attempts)
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			c.Data, e = decode([]byte(raw))
			if e != nil {
				return e
			}
			c.Token = newID("cleanup-")
			c.Attempts++
			_, e = tx.Exec("UPDATE artifact_cleanups SET attempts=?,lease_token=?,lease_until=? WHERE id=?", c.Attempts, c.Token, epoch()+300, c.ID)
			found = e == nil
			return e
		})
		if err != nil || !found {
			return err
		}
		err = p.cleanHistoryArtifacts(ctx, c)
		// The lease token fences a cleanup that raced a late runner callback.
		if err == nil {
			_, err = p.DB.ExecContext(ctx, "DELETE FROM artifact_cleanups WHERE id=? AND lease_token=?", c.ID, c.Token)
		} else {
			_, saveErr := p.DB.ExecContext(ctx, "UPDATE artifact_cleanups SET lease_token='',lease_until=0,next_attempt_at=?,last_error='artifact_cleanup_failed' WHERE id=? AND lease_token=?", epoch()+float64(min(300, 1<<min(c.Attempts, 8))), c.ID, c.Token)
			if saveErr != nil {
				return saveErr
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (p *Platform) cleanHistoryArtifacts(ctx context.Context, c historyCleanupClaim) error {
	invalid := func() error { return errors.New("invalid history cleanup scope") }
	if validID(c.ID) != nil || validID(c.ProjectID) != nil || str(c.Data["id"]) != c.ID || str(c.Data["project_id"]) != c.ProjectID {
		return invalid()
	}
	prefixes, ok := c.Data["prefixes"].([]any)
	if !ok || len(prefixes) > 4 {
		return invalid()
	}
	for _, v := range prefixes {
		prefix, ok := v.(string)
		if !ok || !validHistoryObjectPrefix(c.ID, c.ProjectID, prefix) {
			return invalid()
		}
	}
	// Share the archive uploader's OS lock: deletion waits for an in-flight upload
	// before sweeping object prefixes, even after its final database save is fenced.
	release, err := p.lockHistoryArtifacts(ctx, c.ID)
	if err != nil {
		return err
	}
	defer release()
	// Older intents without an explicit flag conservatively retain remote work.
	remoteRequired, known := c.Data["remote_required"].(bool)
	if !known {
		remoteRequired = true
	}
	if remoteRequired && p.Config.ArchiveEndpoint == "" {
		return errors.New("artifact store unavailable for pending cleanup")
	}
	if remoteRequired {
		store, err := p.objectStore()
		if err != nil {
			return err
		}
		for _, v := range prefixes {
			if err = store.DeletePrefix(ctx, str(v)); err != nil {
				return err
			}
		}
	}
	// Only a single literal task child of ResultsDir may be removed. RemoveAll
	// does not follow child symlinks; reject a symlink task root altogether.
	root, err := filepath.Abs(p.Config.ResultsDir)
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil || real != root {
		return invalid()
	}
	target := filepath.Join(root, c.ID)
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return invalid()
	}
	return os.RemoveAll(target)
}

func (p *Platform) lockHistoryArtifacts(ctx context.Context, id string) (func(), error) {
	if validID(id) != nil {
		return nil, errors.New("invalid artifact lock scope")
	}
	locks := filepath.Join(p.Config.StateDir, "raw-archive-locks")
	if err := os.MkdirAll(locks, 0755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(locks, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			lock.Close()
			return nil, err
		}
		if !pause(ctx, 50*time.Millisecond) {
			lock.Close()
			return nil, ctx.Err()
		}
	}
	return func() { syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }, nil
}
