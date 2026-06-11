package events

import (
	"testing"

	"github.com/google/uuid"
)

func TestPublishIsOrgScoped(t *testing.T) {
	h := NewHub()
	orgA, orgB := uuid.New(), uuid.New()

	chA, cancelA := h.Subscribe(orgA)
	defer cancelA()
	chB, cancelB := h.Subscribe(orgB)
	defer cancelB()

	h.Publish(orgA, Event{Topic: TopicWorkloads})

	select {
	case ev := <-chA:
		if ev.Topic != TopicWorkloads {
			t.Errorf("topic = %q, want %q", ev.Topic, TopicWorkloads)
		}
	default:
		t.Fatal("org A subscriber should have received the event")
	}
	select {
	case ev := <-chB:
		t.Fatalf("org B must not receive org A's event, got %v", ev)
	default:
	}
}

func TestPublishTopicsFansOut(t *testing.T) {
	h := NewHub()
	org := uuid.New()
	ch, cancel := h.Subscribe(org)
	defer cancel()

	h.PublishTopics(org, TopicWorkloads, TopicQueue, TopicBudgets)

	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case ev := <-ch:
			got[ev.Topic] = true
		default:
			t.Fatalf("expected 3 events, got %d", i)
		}
	}
	for _, want := range []string{TopicWorkloads, TopicQueue, TopicBudgets} {
		if !got[want] {
			t.Errorf("missing topic %q", want)
		}
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	h := NewHub()
	org := uuid.New()

	ch, cancel := h.Subscribe(org)
	if n := h.Subscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
	cancel()
	if n := h.Subscribers(); n != 0 {
		t.Fatalf("subscribers after cancel = %d, want 0", n)
	}
	h.Publish(org, Event{Topic: TopicNodes})
	select {
	case ev := <-ch:
		t.Fatalf("cancelled subscriber must not receive events, got %v", ev)
	default:
	}
	// Double-cancel is safe.
	cancel()
}

func TestSlowConsumerDoesNotBlockPublish(t *testing.T) {
	h := NewHub()
	org := uuid.New()
	_, cancel := h.Subscribe(org) // never drained; buffer is 16
	defer cancel()

	// More events than the buffer holds: Publish must drop, not deadlock.
	for i := 0; i < 100; i++ {
		h.Publish(org, Event{Topic: TopicWorkloads})
	}
}
