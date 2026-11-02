package ratelog

import (
	"testing"
	"time"
)

func TestLimiter_AllowsOneLinePerKeyPerInterval(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(time.Minute, func() time.Time { return now })

	if n, ok := l.Allow("a"); !ok || n != 0 {
		t.Fatalf("first a = %d, %v, want 0, true", n, ok)
	}
	if _, ok := l.Allow("a"); ok {
		t.Fatal("second a inside the interval was allowed")
	}
	if _, ok := l.Allow("a"); ok {
		t.Fatal("third a inside the interval was allowed")
	}
	if n, ok := l.Allow("b"); !ok || n != 0 {
		t.Fatalf("first b = %d, %v, want 0, true", n, ok)
	}

	now = now.Add(time.Minute)
	if n, ok := l.Allow("a"); !ok || n != 2 {
		t.Fatalf("a after the interval = %d, %v, want 2, true", n, ok)
	}
}
