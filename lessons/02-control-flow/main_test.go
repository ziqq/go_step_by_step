package main

import "testing"

func TestIsSuccessStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "lower boundary", status: 200, want: true},
		{name: "upper boundary", status: 299, want: true},
		{name: "redirect", status: 302, want: false},
		{name: "client error", status: 404, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSuccessStatus(test.status); got != test.want {
				t.Fatalf("isSuccessStatus(%d) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestIsRetryableStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "request timeout", status: 408, want: true},
		{name: "rate limit", status: 429, want: true},
		{name: "server error", status: 500, want: true},
		{name: "last server error", status: 599, want: true},
		{name: "not found", status: 404, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableStatus(test.status); got != test.want {
				t.Fatalf("isRetryableStatus(%d) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestRequestDecision(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{status: 201, want: "success"},
		{status: 429, want: "retry"},
		{status: 403, want: "stop"},
	}

	for _, test := range tests {
		if got := requestDecision(test.status); got != test.want {
			t.Fatalf("requestDecision(%d) = %q, want %q", test.status, got, test.want)
		}
	}
}

func TestRetryDelaySeconds(t *testing.T) {
	tests := []struct {
		attempt int
		want    int
	}{
		{attempt: -1, want: 0},
		{attempt: 0, want: 0},
		{attempt: 1, want: 1},
		{attempt: 2, want: 2},
		{attempt: 4, want: 8},
	}

	for _, test := range tests {
		if got := retryDelaySeconds(test.attempt); got != test.want {
			t.Fatalf("retryDelaySeconds(%d) = %d, want %d", test.attempt, got, test.want)
		}
	}
}

func TestSumFirst(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{n: -2, want: 0},
		{n: 0, want: 0},
		{n: 1, want: 1},
		{n: 5, want: 15},
	}

	for _, test := range tests {
		if got := sumFirst(test.n); got != test.want {
			t.Fatalf("sumFirst(%d) = %d, want %d", test.n, got, test.want)
		}
	}
}

func TestIsValidPort(t *testing.T) {
	tests := []struct {
		port int
		want bool
	}{
		{port: 0, want: false},
		{port: 32, want: true},
		{port: 65535, want: true},
		{port: 65536, want: false},
	}

	for _, test := range tests {
		if got := isValidPort(test.port); got != test.want {
			t.Fatalf("isValidPort(%d) = %t, want %t", test.port, got, test.want)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		value   int
		minumum int
		maximum int
		want    int
	}{
		{value: 329, minumum: 100, maximum: 500, want: 329},
		{value: 90, minumum: 100, maximum: 500, want: 100},
		{value: 500, minumum: 100, maximum: 500, want: 500},
	}

	for _, test := range tests {
		if got := clamp(test.value, test.minumum, test.maximum); got != test.want {
			t.Fatalf("clamp(%d) = %d, want %d", test.value, got, test.want)
		}
	}
}
