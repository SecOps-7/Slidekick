package hub

import (
	"testing"
	"time"
)

// next returns the next viewer count announced on ch, failing after a second.
func next(t *testing.T, ch <-chan []byte) string {
	t.Helper()
	select {
	case m := <-ch:
		return string(m)
	case <-time.After(time.Second):
		t.Fatal("no message")
		return ""
	}
}

func TestViewerCount(t *testing.T) {
	h := New()
	watch, stop := h.Subscribe("d1") // the presenter view: listens, doesn't count
	defer stop()
	if h.Viewers("d1") != 0 {
		t.Fatal("a non-viewer counted")
	}

	_, tab1 := h.SubscribeViewer("d1", "dev:a")
	if got := next(t, watch); got != `{"type":"viewers","count":1}` {
		t.Fatalf("first viewer: %s", got)
	}
	_, tab2 := h.SubscribeViewer("d1", "dev:a") // a second tab on the same device
	_, other := h.SubscribeViewer("d1", "dev:b")
	if got := next(t, watch); got != `{"type":"viewers","count":2}` { // no message for the second tab
		t.Fatalf("second device: %s", got)
	}
	_, tv := h.SubscribeViewer("d1", "cast")
	_, tvAgain := h.SubscribeViewer("d1", "cast") // the TV reconnecting before the old connection drops
	if got := next(t, watch); got != `{"type":"viewers","count":3}` || h.Viewers("d1") != 3 {
		t.Fatalf("cast TV: %s, %d", got, h.Viewers("d1"))
	}
	_, elsewhere := h.SubscribeViewer("d2", "dev:a")
	defer elsewhere()
	if h.Viewers("d1") != 3 || h.Viewers("d2") != 1 {
		t.Fatal("decks share a count")
	}

	tab1() // the device still has a tab open
	tvAgain()
	other()
	if got := next(t, watch); got != `{"type":"viewers","count":2}` {
		t.Fatalf("after the other device left: %s", got)
	}
	tab2()
	tv()
	tv() // cancelling twice changes nothing
	if h.Viewers("d1") != 0 || h.Count("d1") != 1 {
		t.Fatalf("after everyone left: %d viewers, %d connections", h.Viewers("d1"), h.Count("d1"))
	}
}
