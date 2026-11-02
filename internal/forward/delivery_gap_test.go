package forward

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
)

// flakyUpstream refuses requests while refuse returns a status other than
// zero, and stores the body of every request it accepts.
type flakyUpstream struct {
	*httptest.Server
	attempts atomic.Int32
	mu       sync.Mutex
	stored   int
}

func newFlakyUpstream(refuse func(attempt int32, sinceFirst time.Duration) int) *flakyUpstream {
	u := &flakyUpstream{}
	var firstAt atomic.Int64
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := u.attempts.Add(1)
		firstAt.CompareAndSwap(0, time.Now().UnixNano())
		since := time.Duration(time.Now().UnixNano() - firstAt.Load())
		_, _ = io.Copy(io.Discard, r.Body)
		if status := refuse(n, since); status != 0 {
			w.WriteHeader(status)
			return
		}
		u.mu.Lock()
		u.stored++
		u.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	return u
}

func (u *flakyUpstream) storedCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stored
}

// postDelta sends a one-probe delta batch, the shape an agent sends for code
// that ran once, and returns the collector's status.
func postDelta(t *testing.T, frontURL string) int {
	t.Helper()
	body, err := proto.Marshal(&otherlodepb.DeltaBatch{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "checkout", ServiceInstanceId: "instance-7", RunId: "run-1"},
		Deltas:   []*otherlodepb.ProbeDelta{{ClassId: 1, ProbeIndex: 0, Kind: otherlodepb.ProbeKind_METHOD, HitsTotal: 1}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(frontURL+ingest.DeltaBatchPath, "application/x-protobuf", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// requireNotLost fails when the collector answered 202 and the upstream
// never stored the payload. An agent that gets 202 never sends the batch
// again, so a 202 is a promise that the payload is kept.
func requireNotLost(t *testing.T, status int, up *flakyUpstream) {
	t.Helper()
	if status != http.StatusAccepted {
		return
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if up.storedCount() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("collector answered 202 but the upstream never stored the payload (upstream attempts = %d); the agent will not send it again", up.attempts.Load())
}

func TestGap_UpstreamPlain500_AcceptedPayloadIsNotLost(t *testing.T) {
	up := newFlakyUpstream(func(attempt int32, _ time.Duration) int {
		if attempt == 1 {
			return http.StatusInternalServerError
		}
		return 0
	})
	defer up.Close()
	front := newFrontCollector(t, up.URL)
	defer front.Close()

	requireNotLost(t, postDelta(t, front.URL), up)
}

func TestGap_Upstream4xx_AcceptedPayloadIsNotLost(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			up := newFlakyUpstream(func(attempt int32, _ time.Duration) int {
				if attempt == 1 {
					return status
				}
				return 0
			})
			defer up.Close()
			front := newFrontCollector(t, up.URL)
			defer front.Close()

			requireNotLost(t, postDelta(t, front.URL), up)
		})
	}
}

func TestGap_UpstreamOutageBeyondRetryBudget_AcceptedPayloadIsNotLost(t *testing.T) {
	outage := 2 * testConfig("").RetryMaxElapsedTime
	up := newFlakyUpstream(func(_ int32, sinceFirst time.Duration) int {
		if sinceFirst < outage {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	defer up.Close()
	front := newFrontCollector(t, up.URL)
	defer front.Close()

	requireNotLost(t, postDelta(t, front.URL), up)
}
