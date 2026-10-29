package processor

import (
	"context"
	"reflect"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
)

func TestProcessors_PassNextSinkNoticesThroughUnchanged(t *testing.T) {
	want := []ingest.Notice{ingest.NoticeEnvironmentNotTracked, ingest.NoticeServiceOverCap}
	res := func() *otherlodepb.ResourceAttributes {
		return &otherlodepb.ResourceAttributes{ServiceName: "svc", ServiceInstanceId: "i1", RunId: "run-1"}
	}
	wrappers := map[string]func(ingest.Sink) ingest.Sink{
		"environment": func(next ingest.Sink) ingest.Sink {
			return NewEnvironment(next, EnvironmentConfig{Value: "prod"}, nil)
		},
		"namespace": func(next ingest.Sink) ingest.Sink {
			return NewNamespace(next, NamespaceConfig{Value: "team-a"}, nil)
		},
		"redaction": func(next ingest.Sink) ingest.Sink {
			return NewRedaction(next, allLiterals(), nil)
		},
	}
	for name, wrap := range wrappers {
		sink := wrap(&recordingSink{notices: want})
		ctx := context.Background()
		calls := map[string]func() ([]ingest.Notice, error){
			"deltas": func() ([]ingest.Notice, error) {
				return sink.AcceptDeltaBatch(ctx, &otherlodepb.DeltaBatch{Resource: res()})
			},
			"manifest": func() ([]ingest.Notice, error) {
				return sink.AcceptManifest(ctx, &otherlodepb.ProbeManifest{Resource: res()})
			},
			"static_baseline": func() ([]ingest.Notice, error) {
				return sink.AcceptStaticBaseline(ctx, &otherlodepb.StaticBaseline{Resource: res()})
			},
			"class_listing": func() ([]ingest.Notice, error) {
				return sink.AcceptClassListing(ctx, &otherlodepb.ClassListing{Resource: res()})
			},
		}
		for payload, call := range calls {
			got, err := call()
			if err != nil {
				t.Fatalf("%s %s: unexpected error: %v", name, payload, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s notices = %q, want %q", name, payload, got, want)
			}
		}
	}
}
