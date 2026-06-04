// Package events is the in-process event bus that lets monolith modules stay
// decoupled without a message broker. V1 is synchronous and in-memory; if a
// module is ever extracted, its Bus is swapped for a real transport — callers
// don't change. Event names are documented on each producing module.
package events

type Event struct {
	Name    string
	Payload any
}

type Handler func(Event)

type Bus interface {
	Publish(Event)
	Subscribe(name string, h Handler)
}

// Canonical event names (producer -> consumers):
const (
	NodeEnrolled      = "node.enrolled"       // inventory -> audit
	InventoryUpdated  = "inventory.updated"   // inventory -> telemetry, workloads
	MetricsIngested   = "metrics.ingested"    // telemetry -> policy
	GPUIdleDetected   = "gpu.idle_detected"   // policy   -> workloads (reclaim), audit
	WorkloadStarted   = "workload.started"    // workloads -> billing, audit
	WorkloadStopped   = "workload.stopped"    // workloads -> billing, audit
	UsageRecorded     = "usage.recorded"      // billing   -> (reporting rollups)
)
