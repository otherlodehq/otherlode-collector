package ingest

import (
	"errors"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The most elements that a payload of each kind may hold. An element is
// every embedded message and every element of a repeated field, at every
// depth, so a call edge inside a branch site inside a probe location
// counts three times and a referenced class name counts once. The
// resource counts as one.
//
// The handler counts a body's elements before it decodes the body, since a
// protobuf body decodes to far more memory than its size. It refuses a
// payload over its limit with 413.
//
// The limits bound the decoded message, and the sink's write takes more
// memory on top of it. Peaks measured through the hosted server's ingest
// route, as the heap above the idle heap with the body, the decoded message
// and the write included, at one element under each limit:
//
//   - delta batch: 499990 deltas in 8 MiB, 140 MiB
//   - manifest: 99990 probe locations in 4 MiB, 165 MiB, and with long
//     method descriptors in 14 MiB, 230 MiB
//   - static baseline: 199990 declared methods in 3 MiB, 167 MiB
//   - class listing: 99990 names in 2 MiB, 12 MiB
//
// A 16 MiB body of long strings costs about as much as the manifest above,
// whatever its element count, so the largest peak of one payload is about
// 230 MiB.
const (
	// MaxDeltaBatchElements is the most elements a delta batch may hold.
	MaxDeltaBatchElements = 500_000
	// MaxManifestElements is the most elements a probe manifest may hold.
	MaxManifestElements = 100_000
	// MaxStaticBaselineElements is the most elements a static baseline
	// chunk may hold.
	MaxStaticBaselineElements = 200_000
	// MaxClassListingElements is the most elements a class listing chunk
	// may hold.
	MaxClassListingElements = 100_000
)

// tooManyElementsReason is the metrics reason label for a payload refused
// for its element count.
const tooManyElementsReason = "too_many_elements"

// maxNestingDepth bounds the walk of nested messages. The schema nests five
// levels deep, so a payload deeper than this is not an agent's.
const maxNestingDepth = 32

var (
	errTooManyElements = errors.New("payload has too many elements")
	errBadWire         = errors.New("payload is not valid protobuf")
)

// countElements walks body as the wire form of a message described by md
// and returns the number of embedded messages and repeated elements in it,
// at every depth. It allocates none of them. It stops at once when the
// count passes limit and returns errTooManyElements, with the count so far.
// A body that is not valid protobuf gives errBadWire. Fields the descriptor
// does not name are not counted, since the decoder keeps them as raw bytes.
func countElements(body []byte, md protoreflect.MessageDescriptor, limit int) (int, error) {
	w := elementWalker{left: limit}
	err := w.message(body, md, 0)
	return limit - w.left, err
}

type elementWalker struct {
	left int
}

func (w *elementWalker) charge(n int) error {
	w.left -= n
	if w.left < 0 {
		return errTooManyElements
	}
	return nil
}

func (w *elementWalker) message(b []byte, md protoreflect.MessageDescriptor, depth int) error {
	if depth > maxNestingDepth {
		return errTooManyElements
	}
	fields := md.Fields()
	for len(b) > 0 {
		number, wireType, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errBadWire
		}
		b = b[n:]
		n = protowire.ConsumeFieldValue(number, wireType, b)
		if n < 0 {
			return errBadWire
		}
		value := b[:n]
		b = b[n:]
		fd := fields.ByNumber(number)
		if fd == nil {
			continue
		}
		if err := w.field(fd, wireType, value, depth); err != nil {
			return err
		}
	}
	return nil
}

func (w *elementWalker) field(fd protoreflect.FieldDescriptor, wireType protowire.Type, value []byte, depth int) error {
	kind := fd.Kind()
	if wireType != protowire.BytesType {
		if fd.IsList() && scalarKind(kind) {
			return w.charge(1)
		}
		return nil
	}
	content, n := protowire.ConsumeBytes(value)
	if n < 0 {
		return errBadWire
	}
	switch kind {
	case protoreflect.MessageKind:
		if err := w.charge(1); err != nil {
			return err
		}
		return w.message(content, fd.Message(), depth+1)
	case protoreflect.StringKind, protoreflect.BytesKind:
		if fd.IsList() {
			return w.charge(1)
		}
	default:
		if fd.IsList() && scalarKind(kind) {
			return w.charge(packedValues(kind, content))
		}
	}
	return nil
}

func scalarKind(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.MessageKind, protoreflect.GroupKind, protoreflect.StringKind, protoreflect.BytesKind:
		return false
	}
	return true
}

// packedValues returns how many values a packed repeated field of kind
// holds in content.
func packedValues(kind protoreflect.Kind, content []byte) int {
	switch kind {
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return len(content) / 4
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return len(content) / 8
	}
	values := 0
	for _, b := range content {
		if b < 0x80 {
			values++
		}
	}
	return values
}
