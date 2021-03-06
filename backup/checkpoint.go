package backup

import (
	"bytes"
	"context"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/store/tikv"
	"github.com/pingcap/tidb/store/tikv/tikvrpc"
)

// DoCheckpoint returns a checkpoint.
func (backer *Backer) DoCheckpoint(concurrency int) ([]*RangeMeta, error) {
	physical, logical, err := backer.pdClient.GetTS(backer.ctx)

	checkpoint := Timestamp{
		Physical: physical - MaxTxnTimeUse,
		Logical:  logical,
	}

	handler := func(ctx context.Context, r kv.KeyRange) (int, error) {
		return backer.readIndexForRange(ctx, r.StartKey, r.EndKey)
	}
	runner := tikv.NewRangeTaskRunner("read-index-runner", backer.store.(tikv.Storage), concurrency, handler)

	// TODO: support different ranges for different tables' backup
	ranges := []*kv.KeyRange{
		&kv.KeyRange{
			StartKey: []byte(""),
			EndKey:   []byte(""),
		},
	}

	metas := make([]*RangeMeta, len(ranges))
	for _, keyRange := range ranges {
		err := runner.RunOnRange(backer.ctx, keyRange.StartKey, keyRange.EndKey)
		if err != nil {
			return nil, err
		}
		metas = append(metas, &RangeMeta{
			StartKey:   keyRange.StartKey,
			EndKey:     keyRange.EndKey,
			CheckPoint: &checkpoint,
		})
	}
	return metas, nil
}

func (backer *Backer) readIndexForRange(
	ctx context.Context,
	startKey []byte,
	endKey []byte,
) (int, error) {
	// TODO: update github.com/pingcap/tidb/store/tikv/tikvrpc to support ReadIndex
	req := &tikvrpc.Request{
		Type:      tikvrpc.CmdReadIndex,
		ReadIndex: &kvrpcpb.ReadIndexRequest{},
		ToSlave:   true,
	}

	regions := 0
	key := startKey

	for {
		select {
		case <-ctx.Done():
			return regions, errors.New("backup check point canceled")
		default:
		}

		bo := tikv.NewBackoffer(ctx, tikv.ReadIndexMaxBackoff)
		loc, err := backer.store.GetRegionCache().LocateKey(bo, key)
		if err != nil {
			return regions, errors.Trace(err)
		}
		resp, err := backer.store.SendReq(bo, req, loc.Region, tikv.ReadTimeoutMedium)
		if err != nil {
			return regions, errors.Trace(err)
		}
		regionErr, err := resp.GetRegionError()
		if err != nil {
			return regions, errors.Trace(err)
		}
		if regionErr != nil {
			err = bo.Backoff(tikv.BoRegionMiss, errors.New(regionErr.String()))
			if err != nil {
				return regions, errors.Trace(err)
			}
			continue
		}

		readIndexResp := resp.ReadIndex
		if readIndexResp == nil {
			return regions, errors.Trace(tikv.ErrBodyMissing)
		}

		// seems useless?
		// index := readIndexResp.GetReadIndex()

		regions++
		key = loc.EndKey

		if len(key) == 0 || (len(endKey) != 0 && bytes.Compare(key, endKey) >= 0) {
			break
		}
	}
	return regions, nil
}
