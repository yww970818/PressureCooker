package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
)

const NodeReportTTL = 24 * time.Hour

type StoredNodeReport struct {
	Report       protocol.CoreNodeReport `json:"report"`
	ReceivedAtMS int64                   `json:"received_at_ms"`
}

func (c *Client) nodeReportKey(id string) string { return c.prefix + ":nodes:reports:" + id }

// A delayed packet cannot replace a newer process or extend its liveness. The
// original complete JSON is stored unchanged after the atomic ordering check.
func (c *Client) StoreNodeReport(ctx context.Context, report protocol.CoreNodeReport, receivedAtMS int64) (bool, error) {
	if receivedAtMS < 1 || receivedAtMS > 9007199254740991 {
		return false, errors.New("invalid node receive time")
	}
	if err := protocol.ValidateCoreNodeReport(report, time.UnixMilli(receivedAtMS)); err != nil {
		return false, err
	}
	raw, err := json.Marshal(StoredNodeReport{Report: report, ReceivedAtMS: receivedAtMS})
	if err != nil {
		return false, err
	}
	accepted, err := c.db.Eval(ctx, `
local prior=redis.call('GET',KEYS[1])
if prior then
 local ok,stored=pcall(cjson.decode,prior)
 if not ok or type(stored)~='table' or type(stored.report)~='table' then return redis.error_reply('invalid stored node report') end
 local old=stored.report
 local started=tonumber(old.boot_started_at_ms)
 local sequence=tonumber(old.sequence)
 if not started or not sequence or type(old.boot_id)~='string' then return redis.error_reply('invalid stored node report') end
 if old.boot_id==ARGV[2] then
  if started~=tonumber(ARGV[3]) or tonumber(ARGV[4])<=sequence then return 0 end
 elseif tonumber(ARGV[3])<=started then return 0 end
end
redis.call('SET',KEYS[1],ARGV[1],'PX',ARGV[5])
return 1`, []string{c.nodeReportKey(report.ID)}, string(raw), report.BootID, report.BootStartedAtMS, report.Sequence, NodeReportTTL.Milliseconds()).Int()
	return accepted == 1, err
}
func (c *Client) NodeReports(ctx context.Context, ids []string) (map[string]StoredNodeReport, error) {
	if len(ids) > 256 {
		return nil, errors.New("too many node report IDs")
	}
	keys := []string{}
	unique := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !protocol.ValidID(id) {
			return nil, errors.New("invalid node report ID")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
			keys = append(keys, c.nodeReportKey(id))
		}
	}
	out := map[string]StoredNodeReport{}
	if len(keys) == 0 {
		return out, nil
	}
	values, err := c.db.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		raw, ok := value.(string)
		if !ok || len(raw) > protocol.MaxCoreNodeReportBytes+1024 {
			return nil, errors.New("invalid stored node report")
		}
		var stored StoredNodeReport
		if json.Unmarshal([]byte(raw), &stored) != nil || stored.Report.ID != unique[index] || stored.ReceivedAtMS < 1 || stored.ReceivedAtMS > 9007199254740991 {
			return nil, errors.New("invalid stored node report")
		}
		observed, err := time.Parse(time.RFC3339Nano, stored.Report.ObservedAt)
		if err != nil || protocol.ValidateCoreNodeReport(stored.Report, observed) != nil {
			return nil, errors.New("invalid stored node report")
		}
		out[unique[index]] = stored
	}
	return out, nil
}
func (c *Client) NodeObservationEpoch(ctx context.Context) (string, error) {
	token, err := c.db.Get(ctx, c.prefix+":lease").Result()
	if errors.Is(err, redis.Nil) {
		return protocol.NodeObservationEpoch(""), nil
	}
	if err != nil {
		return "", err
	}
	return protocol.NodeObservationEpoch(token), nil
}
