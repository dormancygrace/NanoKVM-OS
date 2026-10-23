package direct

import (
	"NanoKVM-Server/service/stream"
	"testing"
)

// The fake subscriptions here are zero values: a stream loop started on one
// (direct's run, a client's writeLoop) blocks for the rest of the test
// binary. That is harmless and deliberate; the tests check the manager's
// state, not the loops.
func fakeEndedSubscriptions(t *testing.T, ended map[*stream.VideoSubscription]bool) (*[]*client, *[]*stream.VideoSubscription) {
	t.Helper()
	var notified []*client
	var started []*stream.VideoSubscription
	previousSubscribe, previousEnded, previousClose, previousNotify := subscribeVideo, subscriptionEnded, closeSubscription, notifyEndedClients
	subscribeVideo = func(stream.EncoderConfig) (*stream.VideoSubscription, error) {
		subscription := &stream.VideoSubscription{}
		started = append(started, subscription)
		return subscription, nil
	}
	subscriptionEnded = func(s *stream.VideoSubscription) bool { return ended[s] }
	closeSubscription = func(s *stream.VideoSubscription) { ended[s] = true }
	notifyEndedClients = func(clients []*client) { notified = append(notified, clients...) }
	t.Cleanup(func() {
		subscribeVideo, subscriptionEnded, closeSubscription, notifyEndedClients = previousSubscribe, previousEnded, previousClose, previousNotify
	})
	return &notified, &started
}

// The direct transport had the same window as WebRTC: a viewer joining while
// an ended subscription was still attached met a conflict or a dead stream.
func TestADirectJoinOnAnEndedSubscriptionStartsOver(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH265, stream.VideoCodecH264} {
		old := &stream.VideoSubscription{}
		notified, started := fakeEndedSubscriptions(t, map[*stream.VideoSubscription]bool{old: true})
		s := newStreamer()
		stale := newClient(nil)
		s.mutex.Lock()
		s.subscription, s.config = old, stream.EncoderConfig{Codec: stream.VideoCodecH264}
		s.clients[stale] = struct{}{}
		s.updateClientSnapshotLocked()
		s.mutex.Unlock()

		joining := newClient(nil)
		if err := s.addClient(joining, stream.EncoderConfig{Codec: codec}); err != nil {
			t.Fatalf("%s: join refused: %v", codec, err)
		}
		if len(*started) != 1 || s.subscription != (*started)[0] {
			t.Fatalf("%s: no new subscription", codec)
		}
		if _, ok := s.clients[joining]; !ok || len(s.clients) != 1 || s.config.Codec != codec {
			t.Fatalf("%s: clients %v config %v", codec, s.clients, s.config)
		}
		if len(*notified) != 1 || (*notified)[0] != stale || !subscriptionEnded(old) {
			t.Fatalf("%s: the old viewer or subscription was not ended", codec)
		}
	}
}

func TestAnEndedDirectSubscriptionTakesEveryClient(t *testing.T) {
	fakeEndedSubscriptions(t, map[*stream.VideoSubscription]bool{})
	s := newStreamer()
	old := &stream.VideoSubscription{}
	first, late := newClient(nil), newClient(nil)
	s.mutex.Lock()
	s.subscription, s.config = old, stream.EncoderConfig{Codec: stream.VideoCodecH264}
	s.clients[first], s.clients[late] = struct{}{}, struct{}{}
	clients := s.endSubscriptionLocked(old)
	s.mutex.Unlock()
	if len(clients) != 2 || s.subscription != nil || len(s.clients) != 0 || s.config != (stream.EncoderConfig{}) {
		t.Fatalf("detached %d, streamer %+v", len(clients), s)
	}
}
