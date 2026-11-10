package httpapi

import (
	"testing"
	"time"

	"zeta/portal/internal/domain"
)

func TestShouldReapEnvironment(t *testing.T) {
	active := domain.Environment{ID: "env", Active: true}
	cases := []struct {
		name    string
		env     domain.Environment
		busy    bool
		idle    time.Duration
		timeout time.Duration
		want    bool
	}{
		{"busy client", active, true, time.Hour, time.Minute, false},
		{"idle past timeout", active, false, time.Hour, time.Minute, true},
		{"idle under timeout", active, false, time.Second, time.Minute, false},
		{"already scaled down", domain.Environment{ID: "env"}, false, time.Hour, time.Minute, false},
		{"reaper disabled", active, false, time.Hour, 0, false},
	}
	for _, tc := range cases {
		if got := shouldReapEnvironment(tc.env, tc.busy, tc.idle, tc.timeout); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
