package backup

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/pd/client"
	"github.com/pingcap/tidb/store/tikv"
	"github.com/pingcap/tidb/store/tikv/oracle"
)

// Backer backups a TiDB/TiKV cluster.
type Backer struct {
	ctx      context.Context
	store    tikv.Storage
	pdClient pd.Client
	pdHTTP   struct {
		addrs []string
		cli   *http.Client
	}
}

// NewBacker creates a new Backer.
func NewBacker(ctx context.Context, pdAddrs string) (*Backer, error) {
	driver := tikv.Driver{}
	store, err := driver.Open(fmt.Sprintf("tikv://%s", pdAddrs))
	addrs := strings.Split(pdAddrs, ",")
	// TODO: reuse the pd-client in store
	pdClient, err := pd.NewClient(addrs, pd.SecurityOption{})
	if err != nil {
		return nil, errors.Trace(err)
	}
	return &Backer{
		ctx:      ctx,
		store:    store,
		pdClient: pdClient,
		pdHTTP: struct {
			addrs []string
			cli   *http.Client
		}{
			addrs: addrs,
			cli:   &http.Client{Timeout: 30 * time.Second},
		},
	}, nil
}

// GetClusterVersion returns the current cluster version.
func (backer *Backer) GetClusterVersion() (string, error) {
	// TODO: maybe add cluster-version api to pd client?
	var clusterVersionPrefix = "pd/api/v1/config/cluster-version"

	get := func(addr string) (string, error) {
		if addr != "" && !strings.HasPrefix("http", addr) {
			addr = "http://" + addr
		}
		u, err := url.Parse(addr)
		if err != nil {
			return "", errors.Trace(err)
		}
		url := fmt.Sprintf("%s/%s", u, clusterVersionPrefix)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", errors.Trace(err)
		}
		resp, err := backer.pdHTTP.cli.Do(req)
		if err != nil {
			return "", errors.Trace(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			res, _ := ioutil.ReadAll(resp.Body)
			return "", errors.Errorf("[%d] %s", resp.StatusCode, res)
		}

		r, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", errors.Trace(err)
		}
		return string(r), nil
	}

	var err error
	for _, addr := range backer.pdHTTP.addrs {
		var v string
		var e error
		if v, e = get(addr); e != nil {
			err = errors.Trace(err)
			continue
		}
		return v, nil
	}

	return "", err
}

// GetGCSafePoint returns the current gc safe point.
// TODO: Some cluster may not enable distributed GC.
func (backer *Backer) GetGCSafePoint() (Timestamp, error) {
	// If the given safePoint is less than the current one, it will not be updated and return the old safePoint.
	// We use this API to get gc safe point.
	safePoint, err := backer.pdClient.UpdateGCSafePoint(backer.ctx, 0)
	println(safePoint)
	if err != nil {
		return Timestamp{}, errors.Trace(err)
	}
	return decodeTs(safePoint), nil
}

const physicalShiftBits = 18

func decodeTs(ts uint64) Timestamp {
	physical := oracle.ExtractPhysical(ts)
	logical := ts - (uint64(physical) << physicalShiftBits)
	return Timestamp{
		Physical: physical,
		Logical:  int64(logical),
	}
}

func encodeTs(tp Timestamp) uint64 {
	return uint64((tp.Physical << physicalShiftBits) + tp.Logical)
}
