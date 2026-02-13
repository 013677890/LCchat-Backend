package deviceactive

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"
)

const (
	// DefaultShardCount 默认分片数量。
	DefaultShardCount = 64
	// DefaultUpdateInterval 默认节流间隔（超过该时间才会再次入缓冲 map）。
	DefaultUpdateInterval = 8 * time.Minute
	// DefaultFlushInterval 默认批量消费周期。
	DefaultFlushInterval = 4 * time.Minute
	// DefaultWorkerCount 默认消费者协程数。
	DefaultWorkerCount = 8
	// DefaultQueueSize 默认批量任务队列容量。
	DefaultQueueSize = 8192
)

var errBatchHandlerRequired = errors.New("batch handler is required")

// BatchItem 表示一条需要同步的活跃设备记录。
type BatchItem struct {
	UserUUID string
	DeviceID string
	UnixSec  int64
}

func (b BatchItem) key() string {
	return composeKey(b.UserUUID, b.DeviceID)
}

// BatchHandler 消费一批活跃设备记录。
type BatchHandler func(ctx context.Context, items []BatchItem) error

// Config 定义双 map 同步器配置。
type Config struct {
	ShardCount     int
	UpdateInterval time.Duration
	FlushInterval  time.Duration
	WorkerCount    int
	QueueSize      int
	BatchHandler   BatchHandler
}

type throttleShard struct {
	mu   sync.Mutex
	last map[string]int64 // key=user_uuid:device_id, value=上次放入缓冲 map 的时间戳（unix 秒）
}

// Syncer 维护“分片节流 map + 缓冲 map”并做后台批量消费。
type Syncer struct {
	shards         []throttleShard
	updateInterval time.Duration
	flushInterval  time.Duration
	handler        BatchHandler

	pendingMu sync.Mutex
	pending   map[string]BatchItem

	batchCh chan []BatchItem

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewSyncer 创建并启动同步器。
func NewSyncer(cfg Config) (*Syncer, error) {
	if cfg.BatchHandler == nil {
		return nil, errBatchHandlerRequired
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = DefaultShardCount
	}
	if cfg.UpdateInterval <= 0 {
		cfg.UpdateInterval = DefaultUpdateInterval
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultWorkerCount
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}

	s := &Syncer{
		shards:         make([]throttleShard, cfg.ShardCount),
		updateInterval: cfg.UpdateInterval,
		flushInterval:  cfg.FlushInterval,
		handler:        cfg.BatchHandler,
		pending:        make(map[string]BatchItem),
		batchCh:        make(chan []BatchItem, cfg.QueueSize),
		stopCh:         make(chan struct{}),
	}
	for i := range s.shards {
		s.shards[i] = throttleShard{
			last: make(map[string]int64),
		}
	}

	s.wg.Add(1)
	go s.flushLoop()

	for i := 0; i < cfg.WorkerCount; i++ {
		s.wg.Add(1)
		go s.consumeLoop()
	}

	return s, nil
}

// Touch 在请求进入时调用：
// 1. 分片节流判断是否需要同步；
// 2. 若命中，则写入缓冲 map，等待后台批量消费。
func (s *Syncer) Touch(userUUID, deviceID string, now time.Time) bool {
	if s == nil || userUUID == "" || deviceID == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}

	key := composeKey(userUUID, deviceID)
	unix := now.Unix()

	shard := s.shardFor(key)
	shard.mu.Lock()
	if last, ok := shard.last[key]; ok && now.Sub(time.Unix(last, 0)) < s.updateInterval {
		shard.mu.Unlock()
		return false
	}
	shard.last[key] = unix
	shard.mu.Unlock()

	s.pendingMu.Lock()
	s.pending[key] = BatchItem{
		UserUUID: userUUID,
		DeviceID: deviceID,
		UnixSec:  unix,
	}
	s.pendingMu.Unlock()
	return true
}

// Delete 删除节流 map 与缓冲 map 中的记录。
func (s *Syncer) Delete(userUUID, deviceID string) {
	if s == nil || userUUID == "" || deviceID == "" {
		return
	}

	key := composeKey(userUUID, deviceID)
	shard := s.shardFor(key)
	shard.mu.Lock()
	delete(shard.last, key)
	shard.mu.Unlock()

	s.pendingMu.Lock()
	delete(s.pending, key)
	s.pendingMu.Unlock()
}

// Stop 停止后台协程并尽力消费剩余缓冲数据。
func (s *Syncer) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.wg.Wait()
	})
}

func (s *Syncer) flushLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushOnce()
		case <-s.stopCh:
			s.flushOnce()
			close(s.batchCh)
			return
		}
	}
}

func (s *Syncer) consumeLoop() {
	defer s.wg.Done()

	for batch := range s.batchCh {
		if len(batch) == 0 {
			continue
		}
		if err := s.handler(context.Background(), batch); err != nil {
			// 失败回塞到缓冲 map，等待下次消费。
			s.mergePending(batch)
		}
	}
}

func (s *Syncer) flushOnce() {
	batch := s.swapPending()
	if len(batch) == 0 {
		return
	}

	select {
	case s.batchCh <- batch:
	default:
		// 消费通道满时不丢数据，回塞缓冲 map。
		s.mergePending(batch)
	}
}

func (s *Syncer) swapPending() []BatchItem {
	s.pendingMu.Lock()
	if len(s.pending) == 0 {
		s.pendingMu.Unlock()
		return nil
	}

	old := s.pending
	s.pending = make(map[string]BatchItem)
	s.pendingMu.Unlock()

	items := make([]BatchItem, 0, len(old))
	for _, item := range old {
		items = append(items, item)
	}
	return items
}

func (s *Syncer) mergePending(items []BatchItem) {
	if len(items) == 0 {
		return
	}
	s.pendingMu.Lock()
	for _, item := range items {
		s.pending[item.key()] = item
	}
	s.pendingMu.Unlock()
}

func (s *Syncer) shardFor(key string) *throttleShard {
	return &s.shards[hashString(key)%uint32(len(s.shards))]
}

func composeKey(userUUID, deviceID string) string {
	return userUUID + ":" + deviceID
}

func hashString(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}