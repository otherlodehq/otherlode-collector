package ingest

// NoticeHeader is the response header that carries a notice on a 202. The
// handler writes one value for each notice and the forwarder reads them.
const NoticeHeader = "Otherlode-Notice"

// Notice is a short lowercase name a backend gives an accepted payload to
// say that it stored the payload but judges it differently from the rest.
// This package names a closed set. A newer backend may send another name,
// and the forwarder counts it as unknown.
type Notice string

const (
	// NoticeServiceOverCap says the backend stores the service's payloads
	// but does not track the service, because the tenant's plan tracks no
	// more services.
	NoticeServiceOverCap Notice = "service-over-cap"

	// NoticeEnvironmentNotTracked says the backend stores the environment's
	// payloads but does not track the environment, because the service
	// already watches as many environments as its cap allows.
	NoticeEnvironmentNotTracked Notice = "environment-not-tracked"
)

// Known reports whether n is one of the notices this package defines.
func (n Notice) Known() bool {
	switch n {
	case NoticeServiceOverCap, NoticeEnvironmentNotTracked:
		return true
	default:
		return false
	}
}
