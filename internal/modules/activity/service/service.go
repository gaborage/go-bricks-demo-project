// Package service owns both halves of the product-activity stream demo.
//
// Publish side: ActivityService implements the narrow ActivityRecorder seam the
// products module calls after each successful write, marshalling the event and
// publishing it through the module's single super-stream publisher handle.
//
// Consume side: Project is the typed super-stream consumer's handler. It feeds an
// in-memory projection — per-product counts, per-partition delivery counts and a
// ring of the most recent events — that GET /products/activity renders.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging/streams"
)

const (
	// recentCapacity is the depth of the ring of most-recent events the
	// projection keeps. Bounded on purpose: the projection lives for the whole
	// process, so an unbounded slice would be a leak dressed up as a demo.
	recentCapacity = 50

	// publishTimeout bounds one Publish call. The framework adds NO timeout of
	// its own — the caller's ctx is the only bound it applies — and the client
	// underneath can park a send indefinitely while a producer reconnects, with
	// no timeout covering that wait (wiki/streams.md, "What bounds a publish").
	// Deriving from the caller's ctx only ever SHORTENS it, so an HTTP request's
	// own deadline still wins while a background caller that handed over an
	// unbounded context is bounded here.
	//
	// It is deliberately SHORT because this publish sits on the HTTP write path;
	// see the tradeoff spelled out at publishBytes.
	publishTimeout = 2 * time.Second
)

var (
	// ErrPublisherUnset reports a publish attempted before DeclareStreams handed
	// the module its publisher handle. Distinct from streams.ErrPublisherNotStarted,
	// which the framework raises once the handle exists but Manager.Start has not
	// bound it to a producer yet.
	ErrPublisherUnset = errors.New("activity: stream publisher not declared yet")

	// ErrEmptyRoutingKey reports a publish with no routing key. A super-stream
	// publisher REQUIRES a non-empty key — the framework rejects an empty one
	// rather than defaulting it, because hashing "" is well defined and would
	// pile every message onto a single partition. Caught here so the failure
	// names the caller's mistake instead of the client's.
	ErrEmptyRoutingKey = errors.New("activity: routing key is required on a super-stream publish")
)

// RecentEvent is one projected delivery, carrying both the event and where on the
// super stream it arrived.
type RecentEvent struct {
	ProductID  string    `json:"productId"`
	Action     string    `json:"action"`
	Name       string    `json:"name"`
	Price      float64   `json:"price"`
	OccurredAt time.Time `json:"occurredAt"`
	// Partition is the individual stream the message arrived on
	// (product-activity-0 … -2), which is what msg.Stream names for a super
	// stream — not the super stream itself.
	Partition string `json:"partition"`
	// Offset is the message's offset WITHIN that partition. Offsets are
	// per-partition; they are not comparable across partitions.
	Offset int64 `json:"offset"`
}

// Snapshot is a point-in-time copy of the projection. Every map and slice on it
// is freshly allocated, so a caller may hold or mutate it without touching the
// live state. It doubles as the wire contract of GET /products/activity.
//
// There is deliberately no duplicate counter here. The projection's redelivery
// guard (see apply) is real, but no demo flow can move such a counter — a restart
// clears the guard and the projection together — so publishing one would only
// promise the reader an observation they will never make.
type Snapshot struct {
	SuperStream     string                      `json:"superStream"`
	Partitions      int                         `json:"partitions"`
	ConsumerName    string                      `json:"consumerName"`
	Delivered       int64                       `json:"delivered"`
	ProductCounts   map[string]map[string]int64 `json:"productCounts"`
	PartitionCounts map[string]int64            `json:"partitionCounts"`
	// Recent is newest-first and capped at the requested limit.
	Recent []RecentEvent `json:"recent"`
}

// ActivityService projects the product-activity super stream and publishes onto it.
type ActivityService struct {
	logger logger.Logger

	// publisher is written once, from DeclareStreams, and read from every request
	// goroutine afterwards — so the handoff is an atomic rather than a plain
	// field. DeclareStreams runs during app.Run(), AFTER every module's Init and
	// before the HTTP server is serving, which is why the handle arrives through
	// a setter and never through the constructor.
	publisher atomic.Pointer[streams.Publisher]

	// mu guards every field below it. The typed super-stream consumer calls
	// Project SEQUENTIALLY within one partition but CONCURRENTLY across the three
	// of them — each partition is a separate connection with its own delivery
	// loop — so the projection must be goroutine-safe (streams.Handler).
	mu              sync.RWMutex
	productCounts   map[string]map[string]int64
	partitionCounts map[string]int64
	// lastOffset is the idempotency guard; see Project.
	lastOffset map[string]int64
	recent     []RecentEvent
	recentNext int
	recentLen  int
	delivered  int64
}

// NewActivityService builds an empty projection with no publisher bound yet.
func NewActivityService(log logger.Logger) *ActivityService {
	return &ActivityService{
		logger:          log,
		productCounts:   make(map[string]map[string]int64),
		partitionCounts: make(map[string]int64),
		lastOffset:      make(map[string]int64),
		recent:          make([]RecentEvent, recentCapacity),
	}
}

// SetPublisher binds the handle DeclareSuperStreamPublisher handed the module.
// There is exactly ONE publisher per target per process — a second declaration on
// the same super stream panics at startup — so this service is the single owner
// of the handle, and the poison simulator borrows it through PublishRaw rather
// than declaring one of its own.
func (s *ActivityService) SetPublisher(p *streams.Publisher) {
	s.publisher.Store(p)
}

// RecordActivity mirrors one product write onto the stream lane. It implements
// the products module's ActivityRecorder seam.
//
// It returns nothing on purpose. This is the DEMO lane: the transactional outbox
// stays the reliable path for product lifecycle events (the products service
// commits the row and the outbox event in a single transaction), so a broker
// hiccup here must never fail an HTTP request whose database work already
// committed. The failure is logged at WARN and swallowed.
//
//nolint:gocritic // hugeParam: the value signature is the ActivityRecorder seam the products service depends on.
func (s *ActivityService) RecordActivity(ctx context.Context, evt domain.ProductActivity) {
	data, err := json.Marshal(evt)
	if err == nil {
		err = s.publishBytes(ctx, evt.ProductID, data)
	} else {
		err = fmt.Errorf("marshal product activity: %w", err)
	}
	if err == nil {
		s.logger.Debug().
			Str("productId", evt.ProductID).
			Str("action", evt.Action).
			Msg("Product activity published to super stream")
		return
	}

	s.logger.Warn().
		Err(err).
		Str("productId", evt.ProductID).
		Str("action", evt.Action).
		Str("superStream", domain.SuperStream).
		Msg("Failed to publish product activity — swallowed: the outbox lane remains the reliable path")
}

// PublishRaw publishes arbitrary bytes through the SAME publisher handle
// RecordActivity uses. It exists for the poison simulator, which has to put a
// body on a real partition that the typed consumer cannot decode; every other
// caller should go through RecordActivity.
func (s *ActivityService) PublishRaw(ctx context.Context, routingKey string, data []byte) error {
	return s.publishBytes(ctx, routingKey, data)
}

// publishBytes is the single publish path, so both callers get the same bound,
// the same routing-key rule and the same block-on-confirm semantics.
//
// Publish BLOCKS until the broker confirms (ADR-063): the client's own send is
// asynchronous and swallows write errors, so its nil proves nothing and the
// confirmation is the only truth available. A post-submission error is therefore
// UNKNOWN, not failure — the message may still land. Delivery is at-least-once,
// which is why Project is idempotent.
//
// TRADEOFF — this blocking call is ON the HTTP write path. RecordActivity runs
// inline from the products service, after the row has already committed, so a
// broker in trouble adds up to publishTimeout (2s) to that request's latency
// before the failure is logged and swallowed. Keeping it synchronous is the
// deliberate choice: handing the publish to a goroutine would let two events for
// the same product id race and land out of order on their shared partition,
// destroying the per-key ordering this demo exists to show. So the bound is kept
// short rather than the call made async. The outbox lane is the reliable path for
// product lifecycle events and never blocks on the broker.
func (s *ActivityService) publishBytes(ctx context.Context, routingKey string, data []byte) error {
	pub := s.publisher.Load()
	if pub == nil {
		return ErrPublisherUnset
	}
	if routingKey == "" {
		return ErrEmptyRoutingKey
	}

	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	// RoutingKey is the product id: murmur3 over the partition list makes the
	// placement sticky, so every event for one product lands on one partition and
	// keeps its order there. Order holds within a partition, never across them.
	if err := pub.Publish(ctx, &streams.PublishMessage{Data: data, RoutingKey: routingKey}); err != nil {
		return fmt.Errorf("publish to %s: %w", domain.SuperStream, err)
	}
	return nil
}

// Project is the typed super-stream consumer's handler: the framework has already
// decoded the body into evt and validated its `validate` tags by the time it runs.
// msg.Stream names the PARTITION, not the super stream.
//
// It is called CONCURRENTLY across partitions, so every read and write of the
// projection goes through s.mu.
//
// Returning an error is terminal for the MESSAGE, not for the stream: the failure
// is logged and counted, the offset is NOT committed, and consumption continues.
// Streams have no nack and no redelivery.
//
//nolint:gocritic // hugeParam: the signature is the framework's typed-consumer contract, func(ctx, T, *Message) error.
func (s *ActivityService) Project(_ context.Context, evt domain.ProductActivity, msg *streams.Message) error {
	if msg == nil {
		return errors.New("activity: typed consumer delivered a nil message")
	}

	if duplicate := s.apply(&evt, msg); duplicate {
		s.logger.Debug().
			Str("partition", msg.Stream).
			Int64("offset", msg.Offset).
			Str("productId", evt.ProductID).
			Msg("Skipped a redelivered product activity")
		return nil
	}

	s.logger.Info().
		Str("partition", msg.Stream).
		Int64("offset", msg.Offset).
		Str("productId", evt.ProductID).
		Str("action", evt.Action).
		Msg("Projected product activity")
	return nil
}

// apply folds one delivery into the projection, reporting whether it was a
// redelivery that must not be counted twice.
//
// IDEMPOTENCY (at-least-once): offsets are monotonic within a partition, so a
// delivery at or below the highest offset already projected for THAT partition is
// a redelivery, and counting it again would inflate the tally this demo exists to
// show. One int64 per partition, rather than a seen-set that grows without bound.
//
// Honest scope: this guard covers IN-PROCESS redelivery only, and no walkthrough
// in the README can trip it. A restart does not replay into it. Despite the
// declared streams.OffsetFirst(), a STORED OFFSET ALWAYS WINS: at startup — and
// at each single-active-consumer promotion — the framework asks the broker for
// this consumer name's stored offset per partition and attaches at stored+1
// (framework messaging/streams/manager.go, offsetSpecFor). OffsetFirst applies
// only on the very first run under domain.ConsumerName. So after a graceful
// restart the map and the projection it guards are BOTH empty and the projection
// stays empty until new events arrive — it is not rebuilt from the log. What is
// left for the guard is a crash that loses uncommitted offsets, or a SAC
// re-promotion re-attaching a partition at its last stored offset: both replay a
// handful of already-projected messages into a projection that is still live.
func (s *ActivityService) apply(evt *domain.ProductActivity, msg *streams.Message) (duplicate bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, seen := s.lastOffset[msg.Stream]; seen && msg.Offset <= last {
		return true
	}
	s.lastOffset[msg.Stream] = msg.Offset

	s.delivered++
	s.partitionCounts[msg.Stream]++

	byAction, ok := s.productCounts[evt.ProductID]
	if !ok {
		byAction = make(map[string]int64)
		s.productCounts[evt.ProductID] = byAction
	}
	byAction[evt.Action]++

	s.recent[s.recentNext] = RecentEvent{
		ProductID:  evt.ProductID,
		Action:     evt.Action,
		Name:       evt.Name,
		Price:      evt.Price,
		OccurredAt: evt.OccurredAt,
		Partition:  msg.Stream,
		Offset:     msg.Offset,
	}
	s.recentNext = (s.recentNext + 1) % recentCapacity
	if s.recentLen < recentCapacity {
		s.recentLen++
	}

	return false
}

// Snapshot copies the projection out. limit caps the recent-event slice; a
// non-positive or oversized limit falls back to the ring's full depth.
func (s *ActivityService) Snapshot(limit int) Snapshot {
	if limit <= 0 || limit > recentCapacity {
		limit = recentCapacity
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	products := make(map[string]map[string]int64, len(s.productCounts))
	for id, byAction := range s.productCounts {
		copied := make(map[string]int64, len(byAction))
		for action, n := range byAction {
			copied[action] = n
		}
		products[id] = copied
	}

	partitions := make(map[string]int64, len(s.partitionCounts))
	for partition, n := range s.partitionCounts {
		partitions[partition] = n
	}

	// Newest first: walk backwards from the write cursor.
	size := min(limit, s.recentLen)
	recent := make([]RecentEvent, 0, size)
	for i := range size {
		idx := ((s.recentNext-1-i)%recentCapacity + recentCapacity) % recentCapacity
		recent = append(recent, s.recent[idx])
	}

	return Snapshot{
		SuperStream:     domain.SuperStream,
		Partitions:      domain.PartitionCount,
		ConsumerName:    domain.ConsumerName,
		Delivered:       s.delivered,
		ProductCounts:   products,
		PartitionCounts: partitions,
		Recent:          recent,
	}
}
