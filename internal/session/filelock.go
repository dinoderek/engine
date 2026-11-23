package session

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"time"
)

// The lock on a shared file is the directory <file>.lock, as the
// proper-lockfile convention has it: Claude Code takes it around its config
// saves, which it applies to a fresh read (a save that cannot get it, or one
// at exit, may write without it, which EditFile's stamp check covers). A
// lock older than lockStale is a dead holder's (a live one refreshes it more
// often); engine holds it far less long, so never refreshes it.
const (
	lockStale = 10 * time.Second
	// lockWait bounds the wait for a held lock: past lockStale, so a dead
	// holder's lock is always taken over first.
	lockWait = 3 * lockStale
	// lockPoll is the longest pause between two tries.
	lockPoll = 50 * time.Millisecond
)

// ErrLockHeld is a lock another writer kept holding for all of lockWait.
var ErrLockHeld = errors.New("stayed locked by another writer")

// fileLock takes and releases the lock on one file. now and sleep are the
// clock, which tests replace.
type fileLock struct {
	dir   string
	now   func() time.Time
	sleep func(time.Duration)
}

func lockFor(path string) fileLock {
	return fileLock{dir: path + ".lock", now: time.Now, sleep: time.Sleep}
}

// acquire takes the lock, taking over a stale one, and waits at most
// lockWait for a live holder.
func (l fileLock) acquire() error {
	deadline := l.now().Add(lockWait)
	for {
		err := os.Mkdir(l.dir, 0o700)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("lock %s: %w", l.dir, err)
		}
		// Two writers taking the same stale lock over at once can both end
		// up holding it, as with proper-lockfile itself. That needs a dead
		// holder's lock, and the stamp check narrows, but does not close,
		// the chance that one of their writes is lost.
		if info, err := os.Lstat(l.dir); err == nil && l.now().Sub(info.ModTime()) > lockStale && os.Remove(l.dir) == nil {
			continue
		}
		if !l.now().Before(deadline) {
			return fmt.Errorf("%s %w for %s", l.dir, ErrLockHeld, lockWait)
		}
		l.sleep(lockPoll/2 + rand.N(lockPoll/2))
	}
}

// release drops the lock.
func (l fileLock) release() error {
	if err := os.Remove(l.dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unlock %s: %w", l.dir, err)
	}
	return nil
}
