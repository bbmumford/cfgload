package cfgload

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// reloadDoc is a document type with a list a re-read can leave out and a flag
// its Validate refuses.
type reloadDoc struct {
	Name  string            `yaml:"name" json:"name"`
	Items []string          `yaml:"items,omitempty" json:"items,omitempty"`
	Tags  map[string]string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Bad   bool              `yaml:"bad" json:"bad"`
}

func (d *reloadDoc) Validate() error {
	if d.Bad {
		return errors.New("reloadDoc: refused")
	}
	if d.Name == "" {
		return errors.New("reloadDoc: no name")
	}
	return nil
}

// handed records each value OnReload is given.
type handed struct {
	mu   sync.Mutex
	docs []reloadDoc
	ptrs []*reloadDoc
}

func (h *handed) onReload(v interface{}) {
	d, ok := v.(*reloadDoc)
	if !ok {
		return
	}
	h.mu.Lock()
	h.docs = append(h.docs, *d)
	h.ptrs = append(h.ptrs, d)
	h.mu.Unlock()
}

func (h *handed) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.docs)
}

// waitFor returns the values handed once there are at least n, or what was
// handed after three seconds.
func (h *handed) waitFor(n int) []reloadDoc {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && h.count() < n {
		time.Sleep(5 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]reloadDoc(nil), h.docs...)
}

func writeDoc(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const reloadEvery = 15 * time.Millisecond

// A reload decodes the re-read into an empty value, so a list the re-signed
// document leaves out reads empty, in both forms the loader parses; the value
// OnReload is handed is that new value, which the target then equals.
func TestAReloadReadsAFieldTheReReadLeavesOutAsEmpty(t *testing.T) {
	for _, form := range []struct{ name, first, next string }{
		{"yaml", "name: a\nitems: [x]\ntags: {k: v}\n", "name: b\n"},
		{"json", `{"name":"a","items":["x"],"tags":{"k":"v"}}`, `{"name":"b"}`},
	} {
		t.Run(form.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "doc")
			writeDoc(t, path, form.first)
			var target reloadDoc
			h := &handed{}
			if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload}); err != nil {
				t.Fatal(err)
			}
			defer StopReload(&target)
			writeDoc(t, path, form.next)
			docs := h.waitFor(1)
			if len(docs) == 0 {
				t.Fatal("no reload was handed")
			}
			got := docs[len(docs)-1]
			if got.Name != "b" || len(got.Items) != 0 || len(got.Tags) != 0 {
				t.Fatalf("handed %+v; want name b with no items and no tags, as the re-read says", got)
			}
			StopReload(&target)
			if target.Name != "b" || len(target.Items) != 0 || len(target.Tags) != 0 {
				t.Fatalf("target %+v; want the handed value", target)
			}
			h.mu.Lock()
			last := h.ptrs[len(h.ptrs)-1]
			h.mu.Unlock()
			if last == &target {
				t.Fatal("OnReload was handed the target itself; want a value the loader does not write again")
			}
		})
	}
}

// A re-read that Validate refuses leaves the target as the last accepted
// document left it, is reported to OnReloadError and is not handed.
func TestARefusedReloadLeavesTheValueInForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	writeDoc(t, path, "name: a\nitems: [x]\n")
	var target reloadDoc
	h := &handed{}
	errs := make(chan error, 16)
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload,
		OnReloadError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		}}); err != nil {
		t.Fatal(err)
	}
	defer StopReload(&target)
	writeDoc(t, path, "name: z\nbad: true\n")
	select {
	case <-errs:
	case <-time.After(3 * time.Second):
		t.Fatal("the refused re-read was never reported")
	}
	StopReload(&target)
	if target.Name != "a" || len(target.Items) != 1 || target.Bad {
		t.Fatalf("target %+v after a refused reload; want the accepted document", target)
	}
	if n := h.count(); n != 0 {
		t.Fatalf("%d refused value(s) handed to OnReload", n)
	}
}

// A first load that fails starts the watch anyway: it loads again every
// interval until a load succeeds, and hands that first value to OnReload,
// since the caller was handed none.
func TestAFailedFirstLoadIsRetriedAndItsFirstSuccessHanded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	var target reloadDoc
	h := &handed{}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload}); err == nil {
		t.Fatal("the first load of a missing document succeeded")
	}
	defer StopReload(&target)
	writeDoc(t, path, "name: later\n")
	docs := h.waitFor(1)
	if len(docs) == 0 || docs[0].Name != "later" {
		t.Fatalf("handed %+v; want the document that loaded after the failed first load", docs)
	}
}

// The retry of a failed first load decodes over the caller's value, as the
// first load did, so a caller's default under the document stays.
func TestTheRetryOfAFirstLoadKeepsTheCallersValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	target := reloadDoc{Items: []string{"default"}}
	h := &handed{}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload}); err == nil {
		t.Fatal("the first load of a missing document succeeded")
	}
	defer StopReload(&target)
	writeDoc(t, path, "name: later\n")
	docs := h.waitFor(1)
	if len(docs) == 0 || docs[0].Name != "later" || len(docs[0].Items) != 1 || docs[0].Items[0] != "default" {
		t.Fatalf("handed %+v; want the document over the caller's default items", docs)
	}
}

// StopReload ends a retrying watch too: a document that appears after it is
// never loaded.
func TestStopReloadEndsTheRetryOfAFailedFirstLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	var target reloadDoc
	h := &handed{}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload}); err == nil {
		t.Fatal("the first load of a missing document succeeded")
	}
	StopReload(&target)
	writeDoc(t, path, "name: later\n")
	time.Sleep(10 * reloadEvery)
	if n := h.count(); n != 0 {
		t.Fatalf("a stopped watch handed %d value(s)", n)
	}
	if target.Name != "" {
		t.Fatalf("a stopped watch wrote %+v into the target", target)
	}
}

// A stopped retry loads no more: while the source keeps failing, no load is
// tried after StopReload returns, so a stopped watch fetches nothing.
func TestAStoppedRetryLoadsNoMore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	var target reloadDoc
	var mu sync.Mutex
	failures := 0
	onErr := func(error) {
		mu.Lock()
		failures++
		mu.Unlock()
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return failures
	}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReloadError: onErr}); err == nil {
		t.Fatal("the first load of a missing document succeeded")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && count() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if count() < 2 {
		t.Fatal("the failed first load was not retried")
	}
	StopReload(&target)
	time.Sleep(2 * reloadEvery)
	stopped := count()
	time.Sleep(10 * reloadEvery)
	if n := count(); n != stopped {
		t.Fatalf("a stopped retry went on loading: %d failed load(s) after the stop", n-stopped)
	}
}

// A stopped target leaves no watch state behind, so the loader holds nothing
// per target it no longer watches, and the same target can be watched again.
func TestAStoppedTargetHoldsNoStateAndCanBeWatchedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	writeDoc(t, path, "name: a\n")
	reloadMu.Lock()
	before := len(reloadStates)
	reloadMu.Unlock()
	var target reloadDoc
	for i := 0; i < 3; i++ {
		if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery}); err != nil {
			t.Fatal(err)
		}
		StopReload(&target)
	}
	reloadMu.Lock()
	after := len(reloadStates)
	reloadMu.Unlock()
	if after != before {
		t.Fatalf("watch states: %d before, %d after three stopped watches; want none left", before, after)
	}
	h := &handed{}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h.onReload}); err != nil {
		t.Fatal(err)
	}
	defer StopReload(&target)
	writeDoc(t, path, "name: again\n")
	if docs := h.waitFor(1); len(docs) == 0 {
		t.Fatal("a target watched again after StopReload is never reloaded")
	}
}

// A second Load with EnableReload on a watched target starts no second watch.
func TestASecondWatchOnOneTargetIsNotStarted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	writeDoc(t, path, "name: a\n")
	var target reloadDoc
	h1, h2 := &handed{}, &handed{}
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h1.onReload}); err != nil {
		t.Fatal(err)
	}
	defer StopReload(&target)
	if err := Load(&target, LoadOptions{URL: "file://" + path, EnableReload: true, ReloadInterval: reloadEvery, OnReload: h2.onReload}); err != nil {
		t.Fatal(err)
	}
	writeDoc(t, path, "name: b\n")
	h1.waitFor(1)
	time.Sleep(5 * reloadEvery)
	if h2.count() != 0 {
		t.Fatal("a second Load on a watched target started a second watch")
	}
}

// A first load any step refuses leaves the caller's target as it was, its
// maps and pointed-to values included: the document is decoded into a copy.
func TestARefusedFirstLoadLeavesTheCallersValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	writeDoc(t, path, "name: z\nitems: [q]\ntags: {added: yes}\nbad: true\n")
	target := reloadDoc{Name: "default", Tags: map[string]string{"kept": "1"}}
	if err := Load(&target, LoadOptions{URL: "file://" + path}); err == nil {
		t.Fatal("a refused document loaded")
	}
	if target.Name != "default" || len(target.Items) != 0 || target.Bad || len(target.Tags) != 1 || target.Tags["kept"] != "1" {
		t.Fatalf("target %+v after a refused load; want the caller's value", target)
	}
}

// A first load that succeeds keeps the caller's values for what the document
// leaves out, so a caller can layer one document over another.
func TestAFirstLoadLayersOverTheCallersValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc")
	writeDoc(t, path, "name: over\ntags: {b: two}\n")
	target := reloadDoc{Items: []string{"under"}, Tags: map[string]string{"a": "one"}}
	if err := Load(&target, LoadOptions{URL: "file://" + path}); err != nil {
		t.Fatal(err)
	}
	if target.Name != "over" || len(target.Items) != 1 || target.Tags["a"] != "one" || target.Tags["b"] != "two" {
		t.Fatalf("target %+v; want the document over the caller's value", target)
	}
}

// An empty URL is not retried: there is nothing to load again.
func TestAnEmptyURLStartsNoWatch(t *testing.T) {
	reloadMu.Lock()
	before := len(reloadStates)
	reloadMu.Unlock()
	var target reloadDoc
	if err := Load(&target, LoadOptions{EnableReload: true, ReloadInterval: reloadEvery}); err == nil {
		t.Fatal("a load with no URL succeeded")
	}
	reloadMu.Lock()
	after := len(reloadStates)
	reloadMu.Unlock()
	if after != before {
		t.Fatal("a load with no URL left a watch")
	}
}
