package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
)

// Keep the original global key occupied while scoped leases exist, fencing
// deployed clients which still acquire it with SET NX. This is not a token.
const scopedLeaseSentinel = "\x00pressurecooker:worker-leases:v1"

// Every operation observes and updates the complete lease set atomically. The
// expiry index bounds cleanup to known leases rather than scanning Redis keys.
// All scoped state uses absolute server deadlines; its retention and the legacy
// sentinel expire with the last scoped lease even when every owner disappears.
var workerLeaseScript = redis.NewScript(`
local operation,token,identity,ttl,requested,sentinel=ARGV[1],ARGV[2],ARGV[3],tonumber(ARGV[4]),cjson.decode(ARGV[5]),ARGV[6]
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local function marker(id)
 local raw=redis.call('HGET',KEYS[2],id)
 if not raw then return nil end
 local ok,value=pcall(cjson.decode,raw)
 if not ok or type(value)~='table' or type(value.token)~='string' or type(value.workers)~='table' or #value.workers==0 then return nil end
 for _,worker in ipairs(value.workers) do
  if type(worker)~='string' then return nil end
 end
 return value
end
local function remove(id,value)
 if value then
  for _,worker in ipairs(value.workers) do
   if redis.call('HGET',KEYS[3],worker)==value.token then redis.call('HDEL',KEYS[3],worker) end
  end
 end
 redis.call('HDEL',KEYS[2],id)
 redis.call('ZREM',KEYS[4],id)
end
for _,id in ipairs(redis.call('ZRANGEBYSCORE',KEYS[4],'-inf',now)) do remove(id,marker(id)) end
local function retention()
 local latest=redis.call('ZREVRANGE',KEYS[4],0,0,'WITHSCORES')
 if #latest==0 then
  redis.call('DEL',KEYS[2],KEYS[3],KEYS[4])
  if redis.call('GET',KEYS[1])==sentinel then redis.call('DEL',KEYS[1]) end
 else
  local deadline=tonumber(latest[2])
  for i=2,4 do redis.call('PEXPIREAT',KEYS[i],deadline) end
  local global=redis.call('GET',KEYS[1])
  if not global or global==sentinel then redis.call('SET',KEYS[1],sentinel,'PXAT',deadline) end
 end
end
retention()
local global=redis.call('GET',KEYS[1])
local function complete(id,value)
 if not value then return false end
 local deadline=tonumber(redis.call('ZSCORE',KEYS[4],id))
 if not deadline or deadline<=now then return false end
 if global and global~=sentinel then return false end
 for _,worker in ipairs(value.workers) do
  if redis.call('HGET',KEYS[3],worker)~=value.token then return false end
 end
 return true
end
if operation=='epoch' then
 if global and global~=sentinel then return {global} end
 local tokens={}
 for _,id in ipairs(redis.call('ZRANGE',KEYS[4],0,-1)) do
  local value=marker(id)
  if complete(id,value) then table.insert(tokens,value.token) end
 end
 return tokens
end
if operation=='acquire' then
 if #requested==0 then
  if global or redis.call('ZCARD',KEYS[4])>0 then return 0 end
  redis.call('SET',KEYS[1],token,'PX',ttl)
  return 1
 end
 if (global and global~=sentinel) or redis.call('HEXISTS',KEYS[2],identity)==1 then return 0 end
 for _,worker in ipairs(requested) do
  if redis.call('HEXISTS',KEYS[3],worker)==1 then return 0 end
 end
 redis.call('HSET',KEYS[2],identity,cjson.encode({token=token,workers=requested}))
 redis.call('ZADD',KEYS[4],now+ttl,identity)
 for _,worker in ipairs(requested) do redis.call('HSET',KEYS[3],worker,token) end
 retention()
 return 1
end
local value=marker(identity)
if operation=='release' then
 if global==token then redis.call('DEL',KEYS[1]) end
 if value and value.token==token then remove(identity,value) end
 retention()
 return 1
end
if global==token then
 if operation=='renew' then return redis.call('PEXPIRE',KEYS[1],ttl) end
 return 1
end
if not value or value.token~=token or not complete(identity,value) then return 0 end
if operation=='renew' then
 redis.call('ZADD',KEYS[4],now+ttl,identity)
 retention()
 return 1
end
local covered={}
for _,worker in ipairs(value.workers) do covered[worker]=true end
for _,worker in ipairs(requested) do if not covered[worker] then return 0 end end
return 1
`)

func normalizedLeaseWorkers(workers []string) ([]string, error) {
	unique := make(map[string]bool, len(workers))
	normalized := make([]string, 0, len(workers))
	for _, worker := range workers {
		if !protocol.ValidID(worker) {
			return nil, errors.New("invalid worker lease ID")
		}
		if !unique[worker] {
			unique[worker] = true
			normalized = append(normalized, worker)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

func (c *Client) leaseOperation(ctx context.Context, operation, token string, workers []string) *redis.Cmd {
	identity := sha256.Sum256([]byte(token))
	if workers == nil {
		workers = []string{}
	}
	encoded, _ := json.Marshal(workers)
	return workerLeaseScript.Run(ctx, c.db, []string{c.prefix + ":lease", c.prefix + ":leases:tokens", c.prefix + ":leases:workers", c.prefix + ":leases:expires"}, operation, token, hex.EncodeToString(identity[:]), LeaseTTL.Milliseconds(), string(encoded), scopedLeaseSentinel)
}

// Acquire atomically reserves a complete worker set. With no workers it keeps
// the legacy global lease contract, excluding every scoped worker lease.
func (c *Client) Acquire(ctx context.Context, token string, workers ...string) (bool, error) {
	if token == "" || token == scopedLeaseSentinel {
		return false, errors.New("invalid lease token")
	}
	normalized, err := normalizedLeaseWorkers(workers)
	if err != nil {
		return false, err
	}
	value, err := c.leaseOperation(ctx, "acquire", token, normalized).Int()
	return value == 1, err
}

// OwnsWorkers verifies the full original lease before checking requested
// coverage. A valid legacy global token covers every worker.
func (c *Client) OwnsWorkers(ctx context.Context, token string, workers ...string) (bool, error) {
	normalized, err := normalizedLeaseWorkers(workers)
	if err != nil {
		return false, err
	}
	if token == "" || token == scopedLeaseSentinel {
		return false, nil
	}
	value, err := c.leaseOperation(ctx, "own", token, normalized).Int()
	return value == 1, err
}
func (c *Client) LeaseOwned(ctx context.Context, token string) (bool, error) {
	return c.OwnsWorkers(ctx, token)
}
func (c *Client) Renew(ctx context.Context, token string) (bool, error) {
	if token == "" || token == scopedLeaseSentinel {
		return false, nil
	}
	value, err := c.leaseOperation(ctx, "renew", token, nil).Int()
	return value == 1, err
}
func (c *Client) Release(ctx context.Context, token string) error {
	if token == "" || token == scopedLeaseSentinel {
		return nil
	}
	return c.leaseOperation(ctx, "release", token, nil).Err()
}
