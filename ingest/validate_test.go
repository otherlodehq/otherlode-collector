package ingest

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
)

func validResource() *otherlodepb.ResourceAttributes {
	return &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"}
}

// post sends msg to path and returns the status, the body and the sink.
func post(t *testing.T, path string, msg proto.Message) (int, string, *fakeSink) {
	t.Helper()
	sink := &fakeSink{}
	server := newTestServer(sink)
	defer server.Close()
	resp := postProto(t, server.URL+path, msg)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body), sink
}

func TestHandler_NulInResourceString_RejectedForEveryPayload(t *testing.T) {
	fields := map[string]func(*otherlodepb.ResourceAttributes, string){
		"service_name":        (*otherlodepb.ResourceAttributes).SetServiceName,
		"service_version":     (*otherlodepb.ResourceAttributes).SetServiceVersion,
		"service_instance_id": (*otherlodepb.ResourceAttributes).SetServiceInstanceId,
		"environment":         (*otherlodepb.ResourceAttributes).SetEnvironment,
		"run_id":              (*otherlodepb.ResourceAttributes).SetRunId,
		"service_namespace":   (*otherlodepb.ResourceAttributes).SetServiceNamespace,
		"agent_version":       (*otherlodepb.ResourceAttributes).SetAgentVersion,
	}
	for field, set := range fields {
		res := validResource()
		set(res, "secret\x00value")
		for path, msg := range map[string]proto.Message{
			DeltaBatchPath:     &otherlodepb.DeltaBatch{Resource: res},
			ManifestPath:       &otherlodepb.ProbeManifest{Resource: res},
			StaticBaselinePath: &otherlodepb.StaticBaseline{Resource: res, ScannedAt: 1700000000, ChunkCount: 1},
			ClassListingPath:   &otherlodepb.ClassListing{Resource: res, ListedAt: 1700000000, ChunkCount: 1},
		} {
			t.Run(field+" "+path, func(t *testing.T) {
				status, body, sink := post(t, path, msg)
				if status != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body = %q", status, body)
				}
				if want := "resource." + field + " holds a NUL byte"; !strings.Contains(body, want) {
					t.Errorf("body = %q, want it to contain %q", body, want)
				}
				if strings.Contains(body, "secret") {
					t.Errorf("body quotes the value: %q", body)
				}
				if n := len(sink.deltaBatches) + len(sink.manifests) + len(sink.baselines) + len(sink.listings); n != 0 {
					t.Fatalf("sink received %d payloads, want 0", n)
				}
			})
		}
	}
}

func TestHandleManifest_NulInContent_Accepted(t *testing.T) {
	manifest := &otherlodepb.ProbeManifest{
		Resource: validResource(),
		Probes:   []*otherlodepb.ProbeLocation{{ClassId: 1, ClassName: "com.example.Foo\x00Bar"}},
		Endpoints: []*otherlodepb.EndpointLocation{
			{EndpointId: 1, RouteTemplate: "/a\x00b"},
		},
		SkippedClasses: []*otherlodepb.SkippedClass{{ClassName: "x\x00", Reason: "r\x00"}},
	}
	status, body, sink := post(t, ManifestPath, manifest)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", status, body)
	}
	if len(sink.manifests) != 1 {
		t.Fatalf("sink received %d manifests, want 1", len(sink.manifests))
	}
}

func TestHandler_TimestampRange_ChecksEveryField(t *testing.T) {
	const over = MaxTimestampMillis + 1
	type kase struct {
		path string
		name string
		make func(ms int64) proto.Message
	}
	batch := func(f func(b *otherlodepb.DeltaBatch, ms int64)) func(int64) proto.Message {
		return func(ms int64) proto.Message {
			b := &otherlodepb.DeltaBatch{Resource: validResource()}
			f(b, ms)
			return b
		}
	}
	manifest := func(f func(m *otherlodepb.ProbeManifest, ms int64)) func(int64) proto.Message {
		return func(ms int64) proto.Message {
			m := &otherlodepb.ProbeManifest{Resource: validResource()}
			f(m, ms)
			return m
		}
	}
	cases := []kase{
		{DeltaBatchPath, "deltas[1].first_seen_at", batch(func(b *otherlodepb.DeltaBatch, ms int64) {
			b.Deltas = []*otherlodepb.ProbeDelta{{}, {FirstSeenAt: ms}}
		})},
		{DeltaBatchPath, "endpoint_deltas[1].first_seen_at", batch(func(b *otherlodepb.DeltaBatch, ms int64) {
			b.EndpointDeltas = []*otherlodepb.EndpointDelta{{}, {FirstSeenAt: ms}}
		})},
		{DeltaBatchPath, "dependency_deltas[1].first_loaded_at", batch(func(b *otherlodepb.DeltaBatch, ms int64) {
			b.DependencyDeltas = []*otherlodepb.DependencyDelta{{}, {FirstLoadedAt: ms}}
		})},
		{ManifestPath, "unreported_classes[1].first_seen_unreported_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.UnreportedClasses = []*otherlodepb.UnreportedClass{{}, {FirstSeenUnreportedAt: ms}}
		})},
		{ManifestPath, "failed_classes[1].withheld_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.FailedClasses = []*otherlodepb.FailedClass{{}, {WithheldAt: ms}}
		})},
		{ManifestPath, "classes_failed_to_initialize[1].observed_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.ClassesFailedToInitialize = []*otherlodepb.ClassFailedToInitialize{
				{Reason: otherlodepb.InitializationFailure_VERIFIER_REJECTED},
				{Reason: otherlodepb.InitializationFailure_VERIFIER_REJECTED, ObservedAt: ms},
			}
		})},
		{ManifestPath, "skipped_classes[1].skipped_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.SkippedClasses = []*otherlodepb.SkippedClass{{}, {SkippedAt: ms}}
		})},
		{ManifestPath, "disabled_endpoint_modules[1].disabled_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.DisabledEndpointModules = []*otherlodepb.DisabledEndpointModule{{}, {DisabledAt: ms}}
		})},
		{ManifestPath, "framework_markers[1].seen_at", manifest(func(m *otherlodepb.ProbeManifest, ms int64) {
			m.FrameworkMarkers = []*otherlodepb.FrameworkMarker{{}, {SeenAt: ms}}
		})},
	}
	for _, tc := range cases {
		for _, ms := range []int64{-1, over, 0, 1, MaxTimestampMillis} {
			t.Run(fmt.Sprintf("%s=%d", tc.name, ms), func(t *testing.T) {
				status, body, _ := post(t, tc.path, tc.make(ms))
				if ms == -1 || ms == over {
					if status != http.StatusBadRequest {
						t.Fatalf("status = %d, want 400; body = %q", status, body)
					}
					if want := tc.name + " is out of range"; !strings.Contains(body, want) {
						t.Errorf("body = %q, want it to contain %q", body, want)
					}
					if strings.Contains(body, fmt.Sprint(ms)) {
						t.Errorf("body quotes the value: %q", body)
					}
					return
				}
				if status != http.StatusAccepted {
					t.Fatalf("status = %d, want 202; body = %q", status, body)
				}
			})
		}
	}
}

func TestHandleManifest_FailedToInitializeReason(t *testing.T) {
	for _, tc := range []struct {
		reason   otherlodepb.InitializationFailure
		accepted bool
	}{
		{otherlodepb.InitializationFailure_INITIALIZATION_FAILURE_UNSPECIFIED, false},
		{otherlodepb.InitializationFailure_STATIC_INITIALIZER_THREW, true},
		{otherlodepb.InitializationFailure_VERIFIER_REJECTED, true},
	} {
		t.Run(tc.reason.String(), func(t *testing.T) {
			m := &otherlodepb.ProbeManifest{
				Resource: validResource(),
				ClassesFailedToInitialize: []*otherlodepb.ClassFailedToInitialize{
					{Reason: otherlodepb.InitializationFailure_VERIFIER_REJECTED},
					{ClassName: "a.B", Reason: tc.reason},
				},
			}
			status, body, _ := post(t, ManifestPath, m)
			if tc.accepted {
				if status != http.StatusAccepted {
					t.Fatalf("status = %d, want 202; body = %q", status, body)
				}
				return
			}
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
			if want := "classes_failed_to_initialize[1].reason is not set"; !strings.Contains(body, want) {
				t.Errorf("body = %q, want it to contain %q", body, want)
			}
		})
	}
}

func TestValidateDeltaBatch_SuccessPathAllocatesNothingPerElement(t *testing.T) {
	batch := &otherlodepb.DeltaBatch{Resource: validResource()}
	for range 5000 {
		batch.Deltas = append(batch.Deltas, &otherlodepb.ProbeDelta{FirstSeenAt: 1700000000000})
		batch.EndpointDeltas = append(batch.EndpointDeltas, &otherlodepb.EndpointDelta{FirstSeenAt: 1700000000000})
		batch.DependencyDeltas = append(batch.DependencyDeltas, &otherlodepb.DependencyDelta{FirstLoadedAt: 1700000000000})
	}
	allocs := testing.AllocsPerRun(10, func() {
		if err := validateDeltaBatch(batch); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Errorf("allocs per validation = %v, want a small constant", allocs)
	}
}
