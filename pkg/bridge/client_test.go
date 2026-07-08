package bridge

import "testing"

func TestNilRunMetadataReturnsZeroValue(t *testing.T) {
	var run *Run
	if got := run.Metadata(); got != (RunMetadata{}) {
		t.Fatalf("Metadata = %#v, want zero value", got)
	}
}
