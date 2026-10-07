package domain

import "testing"

func TestValidPlayerID(t *testing.T) {
	if !ValidPlayerID("32410:67:24", "32410") {
		t.Fatal("valid menu tuple rejected")
	}
	for _, id := range []string{"", "32410", "32410:67", "32410:67:24:1", "32411:67:24", "32410:0:24", "32410:067:24", "32410:67:../24", "32410:67:1000000000000000000"} {
		if ValidPlayerID(id, "32410") {
			t.Errorf("accepted %q", id)
		}
	}
}
