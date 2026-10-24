package ingest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

// referenceCount counts the messages and repeated elements of m by
// walking the decoded message, as countElements does from the wire.
func referenceCount(m protoreflect.Message) int {
	n := 0
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList():
			list := v.List()
			n += list.Len()
			if fd.Kind() == protoreflect.MessageKind {
				for i := 0; i < list.Len(); i++ {
					n += referenceCount(list.Get(i).Message())
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			n += 1 + referenceCount(v.Message())
		}
		return true
	})
	return n
}

func sampleManifest() *otherlodepb.ProbeManifest {
	edge := &otherlodepb.CallEdge{ClassName: "a.B", MethodName: "m", MethodDescriptor: "()V"}
	site := &otherlodepb.BranchSite{
		SiteIndex: 1,
		Outcomes: []*otherlodepb.BranchOutcome{
			{BranchIndex: 1, GuardedLines: []*otherlodepb.LineRange{{SourceFile: "B.java", FirstLine: 1, LastLine: 2}}, CaseLabel: []*otherlodepb.ConditionPart{{Text: "x"}}},
			{BranchIndex: 2},
		},
		Condition: []*otherlodepb.ConditionPart{{Text: "a"}, {Text: "b"}},
	}
	return &otherlodepb.ProbeManifest{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "demo", ServiceInstanceId: "i1", RunId: "r1"},
		Probes: []*otherlodepb.ProbeLocation{
			{ClassId: 1, ClassName: "a.B", Calls: []*otherlodepb.CallEdge{edge, edge}, ReferencedClasses: []string{"c.D", "e.F"}, ParameterNames: []string{"p"}, BranchSites: []*otherlodepb.BranchSite{site}, OutsideCaller: &otherlodepb.OutsideCaller{TypeName: "t"}},
			{ClassId: 2},
		},
		SkippedClasses: []*otherlodepb.SkippedClass{{ClassName: "x"}},
		ClassLocations: []*otherlodepb.ClassLocation{{ClassId: 1, InterfaceNames: []string{"i", "j"}}},
		Dependencies:   []*otherlodepb.DependencyLocation{{Identities: []*otherlodepb.DependencyIdentity{{GroupId: "g"}}}},
	}
}

func TestCountElements_MatchesTheDecodedMessage(t *testing.T) {
	manifest := sampleManifest()
	wire, err := proto.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := countElements(wire, manifest.ProtoReflect().Descriptor(), 1000)
	if err != nil {
		t.Fatalf("countElements: %v", err)
	}
	if want := referenceCount(manifest.ProtoReflect()); got != want || want < 20 {
		t.Errorf("count = %d, want %d, the count of the decoded message (at least 20)", got, want)
	}
}

func TestCountElements_ResourceCountsOne(t *testing.T) {
	batch := &otherlodepb.DeltaBatch{Resource: &otherlodepb.ResourceAttributes{ServiceName: "demo"}}
	wire, _ := proto.Marshal(batch)
	if got, err := countElements(wire, batch.ProtoReflect().Descriptor(), 10); err != nil || got != 1 {
		t.Errorf("count = %d, %v, want 1", got, err)
	}
}

func TestCountElements_NestedElementsCountAtEveryDepth(t *testing.T) {
	manifest := &otherlodepb.ProbeManifest{Probes: []*otherlodepb.ProbeLocation{{
		BranchSites: []*otherlodepb.BranchSite{{Outcomes: []*otherlodepb.BranchOutcome{{}}}},
		Calls:       []*otherlodepb.CallEdge{{}},
	}}}
	wire, _ := proto.Marshal(manifest)
	if got, err := countElements(wire, manifest.ProtoReflect().Descriptor(), 100); err != nil || got != 4 {
		t.Errorf("count = %d, %v, want 4: a probe, a call edge, a branch site and an outcome", got, err)
	}
}

func TestCountElements_StopsPastTheLimit(t *testing.T) {
	manifest := sampleManifest()
	wire, _ := proto.Marshal(manifest)
	total := referenceCount(manifest.ProtoReflect())
	md := manifest.ProtoReflect().Descriptor()
	if _, err := countElements(wire, md, total); err != nil {
		t.Errorf("a limit equal to the count: %v, want none", err)
	}
	if _, err := countElements(wire, md, total-1); !errors.Is(err, errTooManyElements) {
		t.Errorf("a limit under the count: %v, want errTooManyElements", err)
	}
}

func TestCountElements_PackedValuesCountOneEach(t *testing.T) {
	loc := &descriptorpb.SourceCodeInfo_Location{Path: []int32{4, 0, 2, 300, 1}}
	wire, err := proto.Marshal(loc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, err := countElements(wire, loc.ProtoReflect().Descriptor(), 100); err != nil || got != 5 {
		t.Errorf("count = %d, %v, want 5 path values", got, err)
	}
}

func TestCountElements_IgnoresUnknownFields(t *testing.T) {
	wire := protowire.AppendTag(nil, 999, protowire.BytesType)
	wire = protowire.AppendBytes(wire, bytes.Repeat([]byte{0}, 1000))
	if got, err := countElements(wire, (&otherlodepb.DeltaBatch{}).ProtoReflect().Descriptor(), 10); err != nil || got != 0 {
		t.Errorf("count = %d, %v, want 0", got, err)
	}
}

func TestCountElements_NestingPastTheMaximumDepthIsTooMany(t *testing.T) {
	nested := &descriptorpb.DescriptorProto{}
	for i := 0; i < maxNestingDepth+1; i++ {
		nested = &descriptorpb.DescriptorProto{NestedType: []*descriptorpb.DescriptorProto{nested}}
	}
	wire, err := proto.Marshal(nested)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := countElements(wire, nested.ProtoReflect().Descriptor(), 1000); !errors.Is(err, errTooManyElements) {
		t.Errorf("%v, want errTooManyElements", err)
	}
}

func TestCountElements_NestingAtTheMaximumDepthIsCounted(t *testing.T) {
	nested := &descriptorpb.DescriptorProto{}
	for i := 0; i < maxNestingDepth; i++ {
		nested = &descriptorpb.DescriptorProto{NestedType: []*descriptorpb.DescriptorProto{nested}}
	}
	wire, _ := proto.Marshal(nested)
	if _, err := countElements(wire, nested.ProtoReflect().Descriptor(), 1000); err != nil {
		t.Errorf("%v, want none", err)
	}
}

func TestCountElements_MalformedBodyIsAnError(t *testing.T) {
	md := (&otherlodepb.DeltaBatch{}).ProtoReflect().Descriptor()
	for name, body := range map[string][]byte{
		"not protobuf":     []byte("not protobuf"),
		"truncated length": {0x12, 0x05, 0x01},
		"truncated tag":    {0x92},
	} {
		if _, err := countElements(body, md, 1000); !errors.Is(err, errBadWire) {
			t.Errorf("%s: %v, want errBadWire", name, err)
		}
	}
}

func TestCountElements_ElementsBeforeAMalformedTailStillCount(t *testing.T) {
	body := emptyElements(2, 100)
	body = append(body, 0x92)
	if _, err := countElements(body, (&otherlodepb.DeltaBatch{}).ProtoReflect().Descriptor(), 50); !errors.Is(err, errTooManyElements) {
		t.Errorf("%v, want errTooManyElements", err)
	}
}

func emptyElements(field protowire.Number, n int) []byte {
	body := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		body = protowire.AppendTag(body, field, protowire.BytesType)
		body = protowire.AppendVarint(body, 0)
	}
	return body
}

type elementCase struct {
	name  string
	path  string
	label string
	field protowire.Number
	limit int
	valid proto.Message
}

func elementCases() []elementCase {
	res := &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"}
	return []elementCase{
		{"delta batch", DeltaBatchPath, "deltas", 2, MaxDeltaBatchElements, &otherlodepb.DeltaBatch{Resource: res}},
		{"manifest", ManifestPath, "manifest", 3, MaxManifestElements, &otherlodepb.ProbeManifest{Resource: res}},
		{"static baseline", StaticBaselinePath, "static_baseline", 2, MaxStaticBaselineElements, &otherlodepb.StaticBaseline{Resource: res, ScannedAt: 1, ChunkCount: 1}},
		{"class listing", ClassListingPath, "class_listing", 2, MaxClassListingElements, &otherlodepb.ClassListing{Resource: res, ListedAt: 1, ChunkCount: 1}},
	}
}

// bodyWithElements returns a valid payload of c's kind that holds exactly
// total elements, by appending empty elements to the marshaled message.
func bodyWithElements(t *testing.T, c elementCase, total int) []byte {
	t.Helper()
	wire, err := proto.Marshal(c.valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	base, err := countElements(wire, c.valid.ProtoReflect().Descriptor(), total)
	if err != nil {
		t.Fatalf("%s: base count: %v", c.name, err)
	}
	return append(wire, emptyElements(c.field, total-base)...)
}

func postBody(h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sinkCalls(s *fakeSink) int {
	return len(s.deltaBatches) + len(s.manifests) + len(s.baselines) + len(s.listings)
}

func TestHandler_ElementLimit_AcceptsAtTheLimitAndRefusesOneOver(t *testing.T) {
	for _, c := range elementCases() {
		t.Run(c.name, func(t *testing.T) {
			sink := &fakeSink{}
			mux := http.NewServeMux()
			NewHandler(sink, nil).Register(mux)

			rec := postBody(mux, c.path, bodyWithElements(t, c, c.limit))
			if rec.Code != http.StatusAccepted || sinkCalls(sink) != 1 {
				t.Fatalf("at the limit: status %d, sink calls %d, want 202 and 1", rec.Code, sinkCalls(sink))
			}

			before := metrics.IngestRejected.Value(c.label, "too_many_elements")
			rec = postBody(mux, c.path, bodyWithElements(t, c, c.limit+1))
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("one over: status %d, want 413", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != "payload has too many elements" {
				t.Errorf("one over: body %q, want %q", got, "payload has too many elements")
			}
			if got := metrics.IngestRejected.Value(c.label, "too_many_elements") - before; got != 1 {
				t.Errorf("one over: %d refusals counted, want 1", got)
			}
			if sinkCalls(sink) != 1 {
				t.Errorf("one over: sink calls %d, want still 1", sinkCalls(sink))
			}
		})
	}
}

func TestHandler_ElementLimit_RunsBeforeTheDecodeSlot(t *testing.T) {
	sink := newGateSink()
	mux := http.NewServeMux()
	NewHandler(sink, nil, WithMaxConcurrentDecodes(1)).Register(mux)

	first := make(chan struct{})
	go func() {
		mux.ServeHTTP(httptest.NewRecorder(), manifestRequest(context.Background(), t))
		close(first)
	}()
	<-sink.entered

	over := bodyWithElements(t, elementCases()[1], MaxManifestElements+1)
	req := httptest.NewRequest(http.MethodPost, ManifestPath, bytes.NewReader(over))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	start := time.Now()
	serveWithin(t, mux, rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || time.Since(start) > time.Second {
		t.Errorf("status %d after %v, want 413 at once while the only slot is held", rec.Code, time.Since(start))
	}
	if got := sink.calls.Load(); got != 1 {
		t.Errorf("sink calls = %d, want 1", got)
	}
	sink.gate <- struct{}{}
	<-first
}

func TestHandler_ElementLimit_LeavesAMalformedBodyTo400(t *testing.T) {
	for _, c := range elementCases() {
		before := metrics.IngestRejected.Value(c.label, "malformed")
		rec := postBody(newMux(&fakeSink{}), c.path, []byte("not protobuf"))
		if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != "malformed "+c.name {
			t.Errorf("%s: status %d, body %q, want 400 malformed", c.name, rec.Code, rec.Body)
		}
		if got := metrics.IngestRejected.Value(c.label, "malformed") - before; got != 1 {
			t.Errorf("%s: %d malformed refusals counted, want 1", c.name, got)
		}
	}
}

func newMux(sink Sink) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(sink, nil).Register(mux)
	return mux
}

func TestElementLimits_MatchTheServer(t *testing.T) {
	for name, c := range map[string]struct{ got, want int }{
		"delta batch":     {MaxDeltaBatchElements, 500_000},
		"manifest":        {MaxManifestElements, 100_000},
		"static baseline": {MaxStaticBaselineElements, 200_000},
		"class listing":   {MaxClassListingElements, 100_000},
	} {
		if c.got != c.want {
			t.Errorf("%s limit = %d, want %d", name, c.got, c.want)
		}
	}
}
