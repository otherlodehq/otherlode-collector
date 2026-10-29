package ingest

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
)

// noticeSink answers every payload with the notices and error it holds.
type noticeSink struct {
	notices []Notice
	err     error
}

func (s noticeSink) AcceptDeltaBatch(context.Context, *otherlodepb.DeltaBatch) ([]Notice, error) {
	return s.notices, s.err
}

func (s noticeSink) AcceptManifest(context.Context, *otherlodepb.ProbeManifest) ([]Notice, error) {
	return s.notices, s.err
}

func (s noticeSink) AcceptStaticBaseline(context.Context, *otherlodepb.StaticBaseline) ([]Notice, error) {
	return s.notices, s.err
}

func (s noticeSink) AcceptClassListing(context.Context, *otherlodepb.ClassListing) ([]Notice, error) {
	return s.notices, s.err
}

func noticeRoutes() map[string]proto.Message {
	res := validResource()
	return map[string]proto.Message{
		DeltaBatchPath:     &otherlodepb.DeltaBatch{Resource: res},
		ManifestPath:       &otherlodepb.ProbeManifest{Resource: res},
		StaticBaselinePath: &otherlodepb.StaticBaseline{Resource: res, ScannedAt: 1700000000, ChunkCount: 1},
		ClassListingPath:   &otherlodepb.ClassListing{Resource: res, ListedAt: 1700000000, ChunkCount: 1},
	}
}

func TestHandler_SinkNotices_SentAsHeaderValuesOnEveryRoute(t *testing.T) {
	cases := []struct {
		name    string
		sink    noticeSink
		status  int
		want    []string
		wantNil bool
	}{
		{name: "one notice", sink: noticeSink{notices: []Notice{NoticeServiceOverCap}}, status: http.StatusAccepted, want: []string{"service-over-cap"}},
		{name: "two notices keep their order", sink: noticeSink{notices: []Notice{NoticeEnvironmentNotTracked, NoticeServiceOverCap}}, status: http.StatusAccepted, want: []string{"environment-not-tracked", "service-over-cap"}},
		{name: "a duplicate is sent once", sink: noticeSink{notices: []Notice{NoticeServiceOverCap, NoticeEnvironmentNotTracked, NoticeServiceOverCap}}, status: http.StatusAccepted, want: []string{"service-over-cap", "environment-not-tracked"}},
		{name: "no notices", sink: noticeSink{}, status: http.StatusAccepted, wantNil: true},
		{name: "an error hides the notices", sink: noticeSink{notices: []Notice{NoticeServiceOverCap}, err: context.Canceled}, status: http.StatusServiceUnavailable, wantNil: true},
	}
	for _, c := range cases {
		for path, msg := range noticeRoutes() {
			t.Run(c.name+" "+path, func(t *testing.T) {
				server := newTestServer(c.sink)
				defer server.Close()
				resp := postProto(t, server.URL+path, msg)
				defer resp.Body.Close()

				if resp.StatusCode != c.status {
					t.Fatalf("status = %d, want %d", resp.StatusCode, c.status)
				}
				got := resp.Header.Values(NoticeHeader)
				if c.wantNil {
					if len(got) != 0 {
						t.Fatalf("%s = %q, want none", NoticeHeader, got)
					}
					return
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Fatalf("%s = %q, want %q", NoticeHeader, got, c.want)
				}
			})
		}
	}
}

func TestNotice_Known_NamesTheClosedSet(t *testing.T) {
	for notice, want := range map[Notice]bool{
		NoticeServiceOverCap:        true,
		NoticeEnvironmentNotTracked: true,
		"quota-soon":                false,
		"":                          false,
		"Service-Over-Cap":          false,
	} {
		if got := notice.Known(); got != want {
			t.Errorf("Notice(%q).Known() = %v, want %v", notice, got, want)
		}
	}
}
