package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestAverage(t *testing.T) {
	result, err := average(15, 2)
	if err != nil {
		t.Fatalf("average(15, 2) returned an unexpected error: %v", err)
	}

	if result != 7.5 {
		t.Fatalf("average(15, 2) = %v, want 7.5", result)
	}
}

func TestAverageReturnsSentinelError(t *testing.T) {
	result, err := average(15, 0)
	if result != 0 {
		t.Fatalf("average(15, 0) result = %v, want 0", result)
	}

	if !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("average(15, 0) error = %v, want ErrDivisionByZero", err)
	}
}

func TestStatusCategory(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "informational", status: 100, want: "informational"},
		{name: "success", status: 201, want: "success"},
		{name: "redirect", status: 302, want: "redirect"},
		{name: "client error", status: 404, want: "client_error"},
		{name: "server error", status: 503, want: "server_error"},
		{name: "last valid status", status: 599, want: "server_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := statusCategory(test.status); err != nil || got != test.want {
				t.Fatalf("statusCategory(%d) = %q, %v; want %q, nil", test.status, got, err, test.want)
			}
		})
	}
}

func TestStatusCategoryRejectsInvalidStatus(t *testing.T) {
	for _, status := range []int{99, 600} {
		if category, err := statusCategory(status); category != "" || !errors.Is(err, ErrInvalidStatus) {
			t.Fatalf("statusCategory(%d) = %q, %v; want empty category and ErrInvalidStatus", status, category, err)
		}
	}
}

func TestInspectStatusDefersFinish(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		want    string
		wantErr bool
	}{
		{name: "success", status: 200, want: "success"},
		{name: "error", status: 600, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make([]string, 0, 2)
			got, err := inspectStatus(test.status, func(event string) {
				events = append(events, event)
			})

			if (err != nil) != test.wantErr {
				t.Fatalf("inspectStatus(%d) error = %v, want error: %t", test.status, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("inspectStatus(%d) = %q, want %q", test.status, got, test.want)
			}
			if !reflect.DeepEqual(events, []string{"start", "finish"}) {
				t.Fatalf("events = %v, want [start finish]", events)
			}
		})
	}
}

func TestInspectStatusRejectsNilLogger(t *testing.T) {
	if category, err := inspectStatus(200, nil); category != "" || !errors.Is(err, ErrNilLogger) {
		t.Fatalf("inspectStatus(200, nil) = %q, %v; want empty category and ErrNilLogger", category, err)
	}
}
