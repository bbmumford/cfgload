package cfgload_test

import (
	"sync"
	"testing"
	"time"

	"github.com/bbmumford/cfgload"
)

// A subscriber receives updates in the order they were stored, even while an
// earlier delivery is still running.
func TestSubscribersReceiveUpdatesInOrder(t *testing.T) {
	const name = "subscribe-order-test"
	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	unsubscribe, err := cfgload.Subscribe(name, func(v interface{}) {
		n, _ := v.(int)
		if n == 1 {
			<-release
		}
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for i := 1; i <= 5; i++ {
		if err := cfgload.UpdateValue(name, i); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(got) }
	// Give later deliveries the chance to overtake the first, held one.
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline) && count() < 4; {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && count() < 5; {
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 {
		t.Fatalf("received %v, want five updates", got)
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("received %v, want 1 2 3 4 5 in order", got)
		}
	}
}

// After unsubscribing, no further update is delivered.
func TestUnsubscribeStopsDelivery(t *testing.T) {
	const name = "subscribe-stop-test"
	var mu sync.Mutex
	calls := 0
	unsubscribe, err := cfgload.Subscribe(name, func(interface{}) { mu.Lock(); calls++; mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if err := cfgload.UpdateValue(name, 1); err != nil {
		t.Fatalf("cfgload.UpdateValue: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("an unsubscribed callback ran %d times", calls)
	}
}

// Unsubscribing while updates are still queued drops them: none is delivered
// after the unsubscribe returns.
func TestUnsubscribeDropsQueuedUpdates(t *testing.T) {
	const name = "subscribe-drop-test"
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var mu sync.Mutex
	var got []int
	unsubscribe, err := cfgload.Subscribe(name, func(v interface{}) {
		n, _ := v.(int)
		if n == 1 {
			entered <- struct{}{}
			<-release
		}
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfgload.UpdateValue(name, 1); err != nil {
		t.Fatalf("cfgload.UpdateValue: %v", err)
	}
	<-entered
	if err := cfgload.UpdateValue(name, 2); err != nil {
		t.Fatalf("cfgload.UpdateValue: %v", err)
	}
	if err := cfgload.UpdateValue(name, 3); err != nil {
		t.Fatalf("cfgload.UpdateValue: %v", err)
	}
	unsubscribe()
	close(release)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("after unsubscribing, the callback received %v, want only the update already running", got)
	}
}
