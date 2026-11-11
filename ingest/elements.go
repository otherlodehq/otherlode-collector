package ingest

import (
	"errors"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The limits on elements per payload kind. An element is every embedded
// message and every element of a repeated field, at every depth. The resource
// counts as one. The handler refuses a payload over its limit with 413.
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

const tooManyElementsReason = "too_many_elements"

const maxNestingDepth = 32

var (
	errTooManyElements = errors.New("payload has too many elements")
	errBadWire         = errors.New("payload is not valid protobuf")
)

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
