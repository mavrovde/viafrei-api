package buildinfo

import "testing"

func TestMustPlatformStopsOnAMalformedFile(t *testing.T) {
	if got := mustPlatform("2.5.21"); got != "2.5.21" {
		t.Fatalf("mustPlatform = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a malformed platform-version.txt did not stop the process")
		}
	}()
	mustPlatform("not a version")
}
