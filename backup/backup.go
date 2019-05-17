package backup

import (
	"fmt"
	"os"
	"time"

	"github.com/pingcap/errors"
)

const (
	// GCDefaultLifeTime is the default GC interval.
	GCDefaultLifeTime = time.Minute * 10
	// BackupDefaultInterval is the default backup interval.
	BackupDefaultInterval = GCDefaultLifeTime / 10
)

// Backup backups a TiDB/TiKV cluster.
func (backer *Backer) Backup(interval time.Duration, concurrency int) error {
	if interval >= GCDefaultLifeTime {
		return errors.Errorf("Backup interval is too large %v, must <= %v",
			interval, GCDefaultLifeTime)
	}

	round := 0
	backMeta := BackupMeta{}
	version, err := backer.GetClusterVersion()
	if err != nil {
		return errors.Trace(err)
	}
	backMeta.ClusterVersion = version

	for {
		// Check point
		cps, err := backer.DoCheckpoint(concurrency)
		if err != nil {
			return errors.Trace(err)
		}
		backMeta.Ranges = cps

		// GC safe point
		sp, err := backer.GetGCSafePoint()
		if err != nil {
			return errors.Trace(err)
		}
		backMeta.SafePoint = &sp

		if encodeTs(sp) >= encodeTs(*cps[0].CheckPoint) {
			fmt.Printf("GC safe point(%d) >= check point(%d)", encodeTs(sp), encodeTs(*cps[0].CheckPoint))
			os.Exit(1)
		}

		round++
		fmt.Printf("backup round %d done, meta: %v", round, backMeta)

		timer := time.NewTimer(interval)
		select {
		case <-backer.ctx.Done():
			return nil
		case <-timer.C:
			return nil
		}
	}
}
