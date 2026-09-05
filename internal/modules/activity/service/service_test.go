package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging/streams"
)

const (
	partition0 = domain.SuperStream + "-0"
	partition1 = domain.SuperStream + "-1"
	partition2 = domain.SuperStream + "-2"

	productA = "product-a"
	productB = "product-b"
)

func newTestService() *ActivityService {
	return NewActivityService(logger.New("error", false))
}

func event(productID, action string) domain.ProductActivity {
	return domain.New(productID, action, "Widget", 9.99)
}

func msg(partition string, offset int64) *streams.Message {
	return &streams.Message{Stream: partition, Offset: offset}
}

func TestProjectCountsPerProductAndPartition(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	deliveries := []struct {
		partition string
		offset    int64
		productID string
		action    string
	}{
		{partition0, 0, productA, domain.ActionCreated},
		{partition0, 1, productA, domain.ActionUpdated},
		{partition0, 2, productA, domain.ActionUpdated},
		{partition1, 0, productB, domain.ActionCreated},
		{partition1, 1, productB, domain.ActionDeleted},
	}

	for _, d := range deliveries {
		if err := svc.Project(ctx, event(d.productID, d.action), msg(d.partition, d.offset)); err != nil {
			t.Fatalf("Project(%s@%d) returned error: %v", d.partition, d.offset, err)
		}
	}

	snapshot := svc.Snapshot(0)

	if snapshot.Delivered != 5 {
		t.Errorf("Delivered = %d, want 5", snapshot.Delivered)
	}
	if got := snapshot.ProductCounts[productA][domain.ActionUpdated]; got != 2 {
		t.Errorf("productA updated count = %d, want 2", got)
	}
	if got := snapshot.ProductCounts[productA][domain.ActionCreated]; got != 1 {
		t.Errorf("productA created count = %d, want 1", got)
	}
	if got := snapshot.ProductCounts[productB][domain.ActionDeleted]; got != 1 {
		t.Errorf("productB deleted count = %d, want 1", got)
	}
	if got := snapshot.PartitionCounts[partition0]; got != 3 {
		t.Errorf("%s count = %d, want 3", partition0, got)
	}
	if got := snapshot.PartitionCounts[partition1]; got != 2 {
		t.Errorf("%s count = %d, want 2", partition1, got)
	}
	if _, ok := snapshot.PartitionCounts[partition2]; ok {
		t.Errorf("%s should not appear: nothing was delivered on it", partition2)
	}

	if snapshot.SuperStream != domain.SuperStream {
		t.Errorf("SuperStream = %q, want %q", snapshot.SuperStream, domain.SuperStream)
	}
	if snapshot.Partitions != domain.PartitionCount {
		t.Errorf("Partitions = %d, want %d", snapshot.Partitions, domain.PartitionCount)
	}
	if snapshot.ConsumerName != domain.ConsumerName {
		t.Errorf("ConsumerName = %q, want %q", snapshot.ConsumerName, domain.ConsumerName)
	}
}

// A stream redelivery replays an offset the projection has already folded in.
// Counting it twice would inflate the tally, so it must be skipped.
func TestProjectSkipsRedeliveredOffsets(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	for _, offset := range []int64{0, 1, 2} {
		if err := svc.Project(ctx, event(productA, domain.ActionCreated), msg(partition0, offset)); err != nil {
			t.Fatalf("Project: %v", err)
		}
	}
	// Replay of offsets already seen on this partition.
	for _, offset := range []int64{0, 1, 2} {
		if err := svc.Project(ctx, event(productA, domain.ActionCreated), msg(partition0, offset)); err != nil {
			t.Fatalf("Project (replay): %v", err)
		}
	}

	snapshot := svc.Snapshot(0)
	if snapshot.Delivered != 3 {
		t.Errorf("Delivered = %d, want 3", snapshot.Delivered)
	}
	if got := snapshot.ProductCounts[productA][domain.ActionCreated]; got != 3 {
		t.Errorf("created count = %d, want 3 (replays must not inflate it)", got)
	}
	if got := len(snapshot.Recent); got != 3 {
		t.Errorf("len(Recent) = %d, want 3", got)
	}
}

// The guard is per partition: offsets are only monotonic within one, so the same
// numeric offset on a different partition is a distinct message.
func TestProjectDedupIsPerPartition(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	for _, partition := range []string{partition0, partition1, partition2} {
		if err := svc.Project(ctx, event(productA, domain.ActionCreated), msg(partition, 0)); err != nil {
			t.Fatalf("Project on %s: %v", partition, err)
		}
	}

	snapshot := svc.Snapshot(0)
	if snapshot.Delivered != 3 {
		t.Errorf("Delivered = %d, want 3", snapshot.Delivered)
	}
}

func TestSnapshotRecentIsNewestFirstAndBounded(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	const total = recentCapacity + 5
	for i := range total {
		evt := event(fmt.Sprintf("product-%03d", i), domain.ActionCreated)
		if err := svc.Project(ctx, evt, msg(partition0, int64(i))); err != nil {
			t.Fatalf("Project: %v", err)
		}
	}

	snapshot := svc.Snapshot(0)
	if got := len(snapshot.Recent); got != recentCapacity {
		t.Fatalf("len(Recent) = %d, want %d", got, recentCapacity)
	}
	if got, want := snapshot.Recent[0].Offset, int64(total-1); got != want {
		t.Errorf("newest offset = %d, want %d", got, want)
	}
	if got, want := snapshot.Recent[recentCapacity-1].Offset, int64(total-recentCapacity); got != want {
		t.Errorf("oldest retained offset = %d, want %d", got, want)
	}
	for i := 1; i < len(snapshot.Recent); i++ {
		if snapshot.Recent[i-1].Offset <= snapshot.Recent[i].Offset {
			t.Fatalf("Recent is not newest-first at index %d", i)
		}
	}
	if snapshot.Recent[0].Partition != partition0 {
		t.Errorf("Partition = %q, want %q", snapshot.Recent[0].Partition, partition0)
	}
}

func TestSnapshotLimit(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	for i := range 10 {
		if err := svc.Project(ctx, event(productA, domain.ActionCreated), msg(partition0, int64(i))); err != nil {
			t.Fatalf("Project: %v", err)
		}
	}

	tests := []struct {
		limit int
		want  int
	}{
		{limit: 3, want: 3},
		{limit: 0, want: 10},                  // fall back to the ring depth
		{limit: -1, want: 10},                 // ditto
		{limit: recentCapacity * 2, want: 10}, // clamped to the ring depth
	}
	for _, tc := range tests {
		if got := len(svc.Snapshot(tc.limit).Recent); got != tc.want {
			t.Errorf("Snapshot(%d) returned %d events, want %d", tc.limit, got, tc.want)
		}
	}
}

// The snapshot must be a defensive copy: a caller mutating it cannot reach the
// live projection the consumer keeps writing to.
func TestSnapshotIsADefensiveCopy(t *testing.T) {
	svc := newTestService()
	if err := svc.Project(context.Background(), event(productA, domain.ActionCreated), msg(partition0, 0)); err != nil {
		t.Fatalf("Project: %v", err)
	}

	first := svc.Snapshot(0)
	first.ProductCounts[productA][domain.ActionCreated] = 999
	first.ProductCounts["injected"] = map[string]int64{domain.ActionCreated: 1}
	first.PartitionCounts[partition0] = 999
	first.Recent[0].ProductID = "mutated"

	second := svc.Snapshot(0)
	if got := second.ProductCounts[productA][domain.ActionCreated]; got != 1 {
		t.Errorf("created count = %d, want 1 — the snapshot aliased live state", got)
	}
	if _, ok := second.ProductCounts["injected"]; ok {
		t.Error("a key added to the snapshot reached the live projection")
	}
	if got := second.PartitionCounts[partition0]; got != 1 {
		t.Errorf("partition count = %d, want 1 — the snapshot aliased live state", got)
	}
	if second.Recent[0].ProductID != productA {
		t.Errorf("Recent[0].ProductID = %q, want %q", second.Recent[0].ProductID, productA)
	}
}

// The typed super-stream consumer calls the handler concurrently across
// partitions. Run under -race.
func TestProjectIsGoroutineSafeAcrossPartitions(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	partitions := []string{partition0, partition1, partition2}
	const perPartition = 200

	var wg sync.WaitGroup
	for _, partition := range partitions {
		wg.Add(1)
		go func(partition string) {
			defer wg.Done()
			for i := range perPartition {
				evt := event(productA, domain.ActionUpdated)
				if err := svc.Project(ctx, evt, msg(partition, int64(i))); err != nil {
					t.Errorf("Project on %s: %v", partition, err)
					return
				}
			}
		}(partition)
	}

	// Readers race the writers: Snapshot takes the read lock while the three
	// delivery loops hold the write lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range perPartition {
			_ = svc.Snapshot(10)
		}
	}()

	wg.Wait()

	snapshot := svc.Snapshot(0)
	want := int64(len(partitions) * perPartition)
	if snapshot.Delivered != want {
		t.Errorf("Delivered = %d, want %d", snapshot.Delivered, want)
	}
	if got := snapshot.ProductCounts[productA][domain.ActionUpdated]; got != want {
		t.Errorf("updated count = %d, want %d", got, want)
	}
	for _, partition := range partitions {
		if got := snapshot.PartitionCounts[partition]; got != perPartition {
			t.Errorf("%s count = %d, want %d", partition, got, perPartition)
		}
	}
}

func TestProjectRejectsNilMessage(t *testing.T) {
	svc := newTestService()
	if err := svc.Project(context.Background(), event(productA, domain.ActionCreated), nil); err == nil {
		t.Fatal("Project(nil message) returned nil, want an error")
	}
}

// Without a bound publisher the recorder must stay silent rather than panic: the
// products service calls it on the request path.
func TestRecordActivityWithoutPublisherIsANoOp(t *testing.T) {
	svc := newTestService()

	svc.RecordActivity(context.Background(), event(productA, domain.ActionCreated))

	if got := svc.Snapshot(0).Delivered; got != 0 {
		t.Errorf("Delivered = %d, want 0", got)
	}
}

func TestPublishRawWithoutPublisher(t *testing.T) {
	svc := newTestService()

	err := svc.PublishRaw(context.Background(), productA, []byte("not-json{{{"))
	if err == nil {
		t.Fatal("PublishRaw returned nil, want ErrPublisherUnset")
	}
	if err != ErrPublisherUnset { //nolint:errorlint // sentinel is returned unwrapped on purpose
		t.Errorf("PublishRaw error = %v, want %v", err, ErrPublisherUnset)
	}
}

// A super-stream publish REQUIRES a non-empty routing key; an empty one would
// otherwise hash onto a single partition.
func TestPublishRawRejectsEmptyRoutingKeyAfterBinding(t *testing.T) {
	svc := newTestService()
	// A non-nil (unbound) handle is enough to get past the ErrPublisherUnset gate
	// and reach the routing-key rule.
	svc.SetPublisher(&streams.Publisher{})

	err := svc.PublishRaw(context.Background(), "", []byte("not-json{{{"))
	if err != ErrEmptyRoutingKey { //nolint:errorlint // sentinel is returned unwrapped on purpose
		t.Errorf("PublishRaw error = %v, want %v", err, ErrEmptyRoutingKey)
	}
}
