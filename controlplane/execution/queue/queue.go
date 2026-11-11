// Package queue transports signed tasks and fences exclusive use of shared engines.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
)

type Config struct {
	Addr, Password string
	DB             int
	Prefix         string
}
type Client struct {
	db     *redis.Client
	prefix string
}
type Message struct {
	ID         string
	SignedTask protocol.SignedTask
	Invalid    bool
}

const Group = "runner-v1"
const LeaseTTL = 45 * time.Second

// go-redis exposes a process-wide logger, not a per-client option. Install it
// once at package initialization: its default pool warnings print dial targets
// and raw errors. Callers log controlled operation codes with task/request IDs.
type safeRedisLogger struct{}

func (safeRedisLogger) Printf(context.Context, string, ...interface{}) {
	log.Print("event=redis_client_warning")
}
func init() { redis.SetLogger(safeRedisLogger{}) }

func New(c Config) (*Client, error) {
	if c.Prefix == "" {
		c.Prefix = "jmeter:v2"
	}
	c.Prefix = strings.TrimSuffix(c.Prefix, ":")
	if c.Addr == "" || c.DB < 0 || strings.ContainsAny(c.Prefix, "*?[] \t\r\n") {
		return nil, errors.New("invalid Redis configuration")
	}
	return &Client{redis.NewClient(&redis.Options{Addr: c.Addr, Password: c.Password, DB: c.DB, DialTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxRetries: 1, ContextTimeoutEnabled: true}), c.Prefix}, nil
}
func (c *Client) Ping(ctx context.Context) error { return c.db.Ping(ctx).Err() }
func (c *Client) Close() error                   { return c.db.Close() }
func (c *Client) stream(cluster string) string   { return c.prefix + ":cluster:" + cluster + ":tasks" }
func (c *Client) Publish(ctx context.Context, s protocol.SignedTask) (string, error) {
	if !protocol.ValidID(s.Task.ClusterID) {
		return "", errors.New("invalid cluster")
	}
	data, e := json.Marshal(s)
	if e != nil {
		return "", e
	}
	return c.db.XAdd(ctx, &redis.XAddArgs{Stream: c.stream(s.Task.ClusterID), Values: map[string]any{"payload": string(data)}}).Result()
}
func (c *Client) EnsureGroup(ctx context.Context, cluster string) error {
	if !protocol.ValidID(cluster) {
		return errors.New("invalid cluster")
	}
	e := c.db.XGroupCreateMkStream(ctx, c.stream(cluster), Group, "0").Err()
	if e != nil && strings.HasPrefix(e.Error(), "BUSYGROUP ") {
		return nil
	}
	return e
}
func decode(v []redis.XMessage) []Message {
	out := make([]Message, 0, len(v))
	for _, m := range v {
		var s protocol.SignedTask
		b, ok := m.Values["payload"].(string)
		out = append(out, Message{ID: m.ID, SignedTask: s, Invalid: !ok})
		r := &out[len(out)-1]
		if len(b) > 4<<20 || json.Unmarshal([]byte(b), &r.SignedTask) != nil {
			r.Invalid = true
		}
	}
	return out
}
func (c *Client) Read(ctx context.Context, cluster, consumer string) ([]Message, error) {
	r, e := c.db.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: consumer, Streams: []string{c.stream(cluster), ">"}, Count: 1, Block: time.Second}).Result()
	if errors.Is(e, redis.Nil) {
		return nil, nil
	}
	if e != nil || len(r) == 0 {
		return nil, e
	}
	return decode(r[0].Messages), nil
}
func (c *Client) Reclaim(ctx context.Context, cluster, consumer, cursor string, idle time.Duration) ([]Message, string, error) {
	v, next, e := c.db.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: c.stream(cluster), Group: Group, Consumer: consumer, MinIdle: idle, Start: cursor, Count: 1}).Result()
	return decode(v), next, e
}

// Ack removes the private property payload after the one consumer group has accepted it.
func (c *Client) Ack(ctx context.Context, cluster, id string) error {
	return c.db.Eval(ctx, `redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); return redis.call('XDEL',KEYS[1],ARGV[2])`, []string{c.stream(cluster)}, Group, id).Err()
}
func Token(runner, task, attempt, nonce string) string {
	return fmt.Sprintf("%s/%s/%s/%s", runner, task, attempt, nonce)
}
