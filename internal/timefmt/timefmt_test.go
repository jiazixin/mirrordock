package timefmt

import (
	"encoding/json/v2"
	"testing"
	"time"
)

func TestTimeContract(t *testing.T) {
	t.Parallel()
	input := time.Date(2026, 10, 30, 9, 2, 3, 456789000, time.FixedZone("fixture", 8*60*60))
	value := From(input)
	encoded, err := json.Marshal(struct {
		At       Time `json:"at"`
		Optional Time `json:"optional,omitzero"`
	}{At: value})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"at":"2026-10-30T01:02:03.456Z"}` {
		t.Fatalf("got %s, want UTC milliseconds with absent optional time", encoded)
	}
	for _, text := range []string{"2026-10-30T01:02:03Z", "2026-10-30T01:02:03.456789Z", "2026-10-30T09:02:03.456+08:00", "2026-10-30T01:02:03,456Z", "2026-10-30T1:02:03.456Z"} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("got success for %q, want invalid time", text)
		}
	}
}

func FuzzTimeRoundTrip(f *testing.F) {
	f.Add("2026-10-30T01:02:03.456Z")
	f.Add("not a date")
	f.Fuzz(func(t *testing.T, text string) {
		value, err := Parse(text)
		if err != nil {
			return
		}
		if got := value.String(); got != text {
			t.Fatalf("got %q, want canonical input %q", got, text)
		}
	})
}
