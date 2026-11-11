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

func requireNotLost(t *testing.T, status int, up *flakyUpstream) {
	t.Helper()
	if status == http.StatusAccepted && up.storedCount() == 0 {
		t.Fatalf("collector answered 202 but the upstream never stored the payload (upstream attempts = %d); the agent will not send it again", up.attempts.Load())
	}
}

func TestGap_UpstreamPlain500_AgentIsNotToldAccepted(t *testing.T) {
	up := newFlakyUpstream(func(attempt int32, _ time.Duration) int {
		if attempt == 1 {
			return http.StatusInternalServerError
		}
		return 0
	})
	defer up.Close()
	front := newFrontCollector(t, up.URL)
	defer front.Close()

	status := postDelta(t, front.URL)
	requireNotLost(t, status, up)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}

	status = postDelta(t, front.URL)
	requireNotLost(t, status, up)
	if status != http.StatusAccepted || up.storedCount() != 1 {
		t.Fatalf("resend: status = %d, stored = %d, want 202 and 1", status, up.storedCount())
	}
}

func TestGap_Upstream4xx_AgentIsNotToldAccepted(t *testing.T) {
	for _, upstreamStatus := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests} {
		t.Run(http.StatusText(upstreamStatus), func(t *testing.T) {
			up := newFlakyUpstream(func(attempt int32, _ time.Duration) int {
				if attempt == 1 {
					return upstreamStatus
				}
				return 0
			})
			defer up.Close()
			front := newFrontCollector(t, up.URL)
			defer front.Close()

			status := postDelta(t, front.URL)
			requireNotLost(t, status, up)
			if status != upstreamStatus {
				t.Fatalf("status = %d, want the upstream's %d", status, upstreamStatus)
			}
		})
	}
}

func TestGap_UpstreamOutage_AgentIsNeverToldAcceptedUntilStored(t *testing.T) {
	const outage = 3
	up := newFlakyUpstream(func(attempt int32, _ time.Duration) int {
		if attempt <= outage {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	defer up.Close()
	front := newFrontCollector(t, up.URL)
	defer front.Close()

	for i := 0; i < outage; i++ {
		status := postDelta(t, front.URL)
		requireNotLost(t, status, up)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("send %d during the outage: status = %d, want %d", i+1, status, http.StatusServiceUnavailable)
		}
	}
	status := postDelta(t, front.URL)
	requireNotLost(t, status, up)
	if status != http.StatusAccepted || up.storedCount() != 1 {
		t.Fatalf("after the outage: status = %d, stored = %d, want 202 and 1", status, up.storedCount())
	}
}

func TestGap_UpstreamSlowerThanTheDeadline_AgentIsNotToldAccepted(t *testing.T) {
	up := newFlakyUpstream(func(attempt int32, _ time.Duration) int { return 0 })
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(5 * time.Second):
			up.Config.Handler.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer up.Close()
	front := newFrontCollector(t, slow.URL)
	defer front.Close()

	status := postDelta(t, front.URL)
	requireNotLost(t, status, up)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}
